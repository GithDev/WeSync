package com.wesync.app

import android.content.Context
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Environment
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import mobile.Mobile
import mobile.PowerHost
import java.io.File

// Single owner of the in-process Go backend lifecycle, the gate's PowerHost,
// and the radio/CPU locks held during a sync.
//
// The backend (Mobile.start) is a process-global singleton shared by two
// independent callers: the UI foreground service (while the activity is open)
// and the WorkManager sync worker (a background wake-up). Coupling "backend up"
// to "service alive" (the old model) broke as soon as a worker could run with
// no service. So we refcount owners here instead: the backend starts on the
// first acquire and is torn down only when the set empties AND the gate has no
// background reason to keep ST alive. This is the ONLY place Mobile.stop is
// called.
//
// It also implements PowerHost: the gate pushes the desired ST run-state and we
// hold/release the MulticastLock (LAN discovery) + partial WakeLock (CPU during
// a Doze-time sync) — process-global, so it doesn't matter whether a service or
// a worker is the live component.
object BackendOwnership : PowerHost {
    private const val TAG = "WeSync.Backend"

    const val OWNER_UI = "ui"
    fun workerOwner(id: String) = "worker:$id"

    // Safety ceiling on the WakeLock so a missed release can't pin the CPU
    // forever. Matches the Android 14 dataSync foreground-service cap, which is
    // the real ceiling on a single background sync anyway.
    private const val WAKELOCK_TIMEOUT_MS = 6L * 60 * 60 * 1000

    private val lock = Any()
    private val owners = mutableSetOf<String>()
    private var appContext: Context? = null
    private var hostRegistered = false
    private var multicastLock: WifiManager.MulticastLock? = null
    private var wakeLock: PowerManager.WakeLock? = null

    // Acquire the backend for [owner], starting it if it isn't already up.
    // Idempotent per owner; safe to call repeatedly.
    fun acquire(ctx: Context, owner: String) {
        val app = ctx.applicationContext
        synchronized(lock) {
            appContext = app
            owners.add(owner)
            if (!hostRegistered) {
                try {
                    Mobile.setPowerHost(this)
                    hostRegistered = true
                } catch (t: Throwable) {
                    Log.w(TAG, "setPowerHost failed", t)
                }
            }
        }
        startBackendIfReady(app)
    }

    // Release [owner]. If nobody else needs the backend AND the gate reports no
    // background reason to keep ST alive (i.e. no sync session still finishing),
    // tear the backend down so the OS can reclaim the process.
    fun release(owner: String) {
        val empty = synchronized(lock) {
            owners.remove(owner)
            owners.isEmpty()
        }
        if (!empty) return
        val stayResident = try {
            Mobile.shouldStayResident()
        } catch (t: Throwable) {
            false
        }
        if (stayResident) {
            // A sync is still finishing with no UI and no worker token left to
            // observe it. Leave the backend up; the next worker run (or app open)
            // will re-acquire and eventually release it once the sync completes.
            Log.i(TAG, "owners empty but sync still active — leaving backend up")
            return
        }
        stopBackend()
    }

    fun isBackendRunning(): Boolean = try {
        Mobile.isRunning()
    } catch (t: Throwable) {
        false
    }

    // Boots the Go backend if it isn't already running and the storage
    // permission Syncthing needs is granted. Idempotent: Mobile.start
    // short-circuits when already running.
    fun startBackendIfReady(ctx: Context) {
        if (Mobile.isRunning()) return
        if (!hasStoragePermission(ctx)) {
            Log.i(TAG, "skipping Mobile.start — storage permission not granted yet")
            return
        }
        val stPath = File(ctx.applicationInfo.nativeLibraryDir, "libsyncthing.so").absolutePath
        if (!File(stPath).exists()) {
            Log.e(TAG, "libsyncthing.so missing at $stPath — APK packaging issue")
            return
        }
        try {
            Mobile.start(ctx.filesDir.absolutePath, stPath, resolveDeviceName(ctx))
            Log.i(TAG, "Mobile.start: ok")
        } catch (t: Throwable) {
            Log.e(TAG, "Mobile.start failed", t)
        }
    }

    private fun stopBackend() {
        Log.i(TAG, "stopping Go backend — no UI and no active sync")
        synchronized(lock) { releaseLocks() }
        try {
            Mobile.stop()
        } catch (t: Throwable) {
            Log.w(TAG, "Mobile.stop failed", t)
        }
    }

    // ── PowerHost: the gate pushes desired ST run-state; we hold/release the
    // radio + CPU. Idempotent and thread-safe (the gate dedupes edges, but the
    // call lands off the main thread so we guard anyway).
    override fun onSyncActive(active: Boolean) {
        synchronized(lock) {
            if (active) acquireLocks() else releaseLocks()
        }
    }

    private fun acquireLocks() {
        val ctx = appContext ?: return
        if (multicastLock == null) {
            try {
                val wifi = ctx.getSystemService(Context.WIFI_SERVICE) as WifiManager
                multicastLock = wifi.createMulticastLock("wesync-sync").apply {
                    setReferenceCounted(false)
                    acquire()
                }
            } catch (t: Throwable) {
                Log.w(TAG, "MulticastLock acquire failed", t)
            }
        }
        if (wakeLock == null) {
            try {
                val pm = ctx.getSystemService(Context.POWER_SERVICE) as PowerManager
                wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "wesync:sync").apply {
                    setReferenceCounted(false)
                    acquire(WAKELOCK_TIMEOUT_MS)
                }
            } catch (t: Throwable) {
                Log.w(TAG, "WakeLock acquire failed", t)
            }
        }
        Log.i(TAG, "sync locks held")
    }

    private fun releaseLocks() {
        multicastLock?.let { if (it.isHeld) try { it.release() } catch (_: Throwable) {} }
        multicastLock = null
        wakeLock?.let { if (it.isHeld) try { it.release() } catch (_: Throwable) {} }
        wakeLock = null
        Log.i(TAG, "sync locks released")
    }

    private fun hasStoragePermission(ctx: Context): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            true
        }
    }

    // Android's kernel hostname is "localhost"; resolve something sensible for
    // the WeSync device name on first backend start.
    private fun resolveDeviceName(ctx: Context): String {
        val sources = listOf<() -> String?>(
            { Settings.Global.getString(ctx.contentResolver, "device_name") },
            { Settings.Secure.getString(ctx.contentResolver, "bluetooth_name") },
        )
        for (src in sources) {
            try {
                val v = src()?.trim().orEmpty()
                if (v.isNotEmpty()) return v
            } catch (_: Throwable) {
            }
        }
        return Build.MODEL.ifBlank { "Android device" }
    }
}
