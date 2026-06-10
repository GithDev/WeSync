package com.wesync.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import mobile.Mobile
import java.io.File

// WeSyncService is the foreground-priority host for the Go backend on
// Android. It is created lazily on demand (TriggerReceiver, MainActivity)
// and self-stops once neither the UI nor an active trigger window needs
// it, so the OS can reclaim the process.
//
// Lifecycle states this service handles:
//
//   MainActivity attached     ─ keep running, UI needs ST + API
//   MainActivity backgrounded ─ scheduleShutdown(GRACE_MS); user might come back
//   Trigger window active     ─ scheduleShutdown(WINDOW_GRACE_MS) — wait it out
//   Neither                   ─ stopSelf → onDestroy → backend.Stop → process dies
//
// The shutdown runnable is the only thing keeping the process pinned
// after the user leaves. When it fires it re-checks state — if a new
// trigger window opened in the meantime, it defers again instead of
// killing an in-flight sync.
class WeSyncService : Service(), mobile.PowerHost {

    private var power: PowerController? = null
    private val main = Handler(Looper.getMainLooper())

    // Radio + CPU held for exactly as long as the gate says ST should run.
    // The gate is the single source of truth: it pushes OnSyncActive(true/
    // false) and we hold/release here. MulticastLock keeps LAN discovery
    // announces flowing (background syncs can't find peers without it);
    // the partial WakeLock keeps the CPU alive through a sync that started
    // from a doze wake-up.
    private var multicastLock: WifiManager.MulticastLock? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private val lockMutex = Any()
    // power.start() registers receivers + kicks off the initial reapply.
    // We only want that *once* per service lifetime — without this guard every
    // "kick the service" intent (CANCEL_SHUTDOWN, MainActivity's post-grant
    // nudge) would re-register receivers and re-run reapply.
    private var powerStarted = false

    private val shutdownRunnable: Runnable = object : Runnable {
        override fun run() {
            // Single source of truth: ask the gate whether ST still needs to be
            // running for a background reason (open session or charging).
            // The gate computes this exactly as it decides ST's own
            // run-state, so the service can't disagree with it — which is what
            // used to let a plugged-in periodic sync die after the grace despite
            // "keep syncing while charging" being on.
            val stayAlive = try {
                Mobile.shouldStayResident()
            } catch (t: Throwable) {
                Log.w(TAG, "shouldStayResident failed", t)
                false
            }
            if (stayAlive) {
                // Re-check shortly so we self-stop the moment that reason ends
                // (session closes, unplugged, leaves trusted wifi) without
                // needing an external kick to fire.
                main.postDelayed(this, WINDOW_GRACE_MS)
                return
            }
            Log.i(TAG, "self-stopping (gate wants ST off, no UI)")
            stopSelf()
        }
    }

    override fun onCreate() {
        super.onCreate()
        ensureNotificationChannel()
        power = PowerController(applicationContext)
        // Register as the gate's power host so it can hold/release the
        // radio + CPU around sync sessions. Re-asserts current state
        // immediately, so a session already in flight grabs its locks.
        try {
            Mobile.setPowerHost(this)
        } catch (t: Throwable) {
            Log.w(TAG, "setPowerHost failed", t)
        }
        // Ownership of the Go backend lives here, not in MainActivity.
        // AlarmManager + TriggerReceiver wakes us in a fresh process when
        // the activity is gone; without this call, Mobile.start would
        // never run and triggers would tick into a dead backend.
        startBackendIfReady()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val notif = buildNotification()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(
                NOTIFICATION_ID,
                notif,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
            )
        } else {
            startForeground(NOTIFICATION_ID, notif)
        }

        // Always re-check whether we can boot the backend. Permissions
        // may have just been granted (MainActivity re-pokes us after
        // returning from system settings); the previous onCreate may
        // have skipped this. Idempotent: Mobile.start short-circuits if
        // already running.
        startBackendIfReady()

        // Record what just woke us. Helps the user verify autonomous
        // wake-ups in Recent activity (e.g. "wake: trigger-rearm" lines
        // before the corresponding "trigger" / "st_start" entries).
        val wakeReason = intent?.action ?: "initial"
        try {
            Mobile.logPowerEvent("wake", "service started — reason=$wakeReason")
        } catch (_: Throwable) {
        }

        when (intent?.action) {
            TriggerReceiver.ACTION_FIRE, TriggerReceiver.ACTION_FIRE_POLL -> {
                // Ensure power listeners are registered and Go has current
                // network + battery state before we ask it to evaluate
                // conditions. Without this, a fresh process start from a
                // FIRE wake-up would have no network state and silently skip.
                if (!powerStarted) {
                    powerStarted = true
                    power?.start()
                }
                if (intent?.action == TriggerReceiver.ACTION_FIRE) {
                    try {
                        Mobile.onTriggerAlarm()
                    } catch (t: Throwable) {
                        Log.w(TAG, "onTriggerAlarm failed", t)
                    }
                    power?.rearmSafetyAlarm()
                } else {
                    try {
                        Mobile.onTriggerPollAlarm()
                    } catch (t: Throwable) {
                        Log.w(TAG, "onTriggerPollAlarm failed", t)
                    }
                    power?.rearmPollAlarm()
                }
                main.removeCallbacks(shutdownRunnable)
                main.postDelayed(shutdownRunnable, WINDOW_GRACE_MS)
            }
            TriggerReceiver.ACTION_REARM -> power?.rearmSafetyAlarm()
            TriggerReceiver.ACTION_REARM_POLL -> power?.rearmPollAlarm()
            ACTION_REFRESH_NETWORK -> power?.refreshNetwork()
            ACTION_CANCEL_SHUTDOWN -> {
                main.removeCallbacks(shutdownRunnable)
                Log.i(TAG, "shutdown cancelled (UI returned)")
            }
            ACTION_SCHEDULE_SHUTDOWN -> {
                val delayMs = intent.getLongExtra(EXTRA_DELAY_MS, GRACE_MS)
                main.removeCallbacks(shutdownRunnable)
                main.postDelayed(shutdownRunnable, delayMs)
                Log.i(TAG, "shutdown scheduled in ${delayMs / 1000}s")
            }
            ACTION_BOOT -> {
                // Device just rebooted, no UI. Start the gate (arms alarms)
                // then schedule the normal self-stop: alarms wake us each time;
                // shouldStayResident keeps us alive while charging or a session is open.
                if (!powerStarted) {
                    powerStarted = true
                    power?.start()
                }
                main.removeCallbacks(shutdownRunnable)
                main.postDelayed(shutdownRunnable, GRACE_MS)
            }
            ACTION_START_BACKEND, null -> {
                if (!powerStarted) {
                    powerStarted = true
                    power?.start()
                }
            }
        }
        return START_STICKY
    }

    // Boots the Go backend if it isn't already running and the user has
    // granted the storage permission Syncthing needs. Idempotent (Go-side
    // Mobile.start short-circuits when already running), safe to call on
    // every service onCreate.
    private fun startBackendIfReady() {
        if (Mobile.isRunning()) return
        if (!hasStoragePermission()) {
            Log.i(TAG, "skipping Mobile.start — storage permission not granted yet")
            return
        }
        val stPath = File(applicationInfo.nativeLibraryDir, "libsyncthing.so").absolutePath
        if (!File(stPath).exists()) {
            Log.e(TAG, "libsyncthing.so missing at $stPath — APK packaging issue")
            return
        }
        val deviceName = resolveDeviceName()
        try {
            Mobile.start(filesDir.absolutePath, stPath, deviceName)
            Log.i(TAG, "Mobile.start: ok")
            // Re-arm now that the backend is actually coming up. The one-shot
            // reapply in power.start() may have started its retry deadline long
            // before this point (on a fresh install power.start runs before
            // storage permission is granted, i.e. before Mobile.start is ever
            // called) and given up waiting for a wake plan. Kicking reapply
            // here restarts that wait the moment the gate can answer, so
            // periodic/scheduled alarms get armed without needing a restart.
            power?.reapply()
        } catch (t: Throwable) {
            Log.e(TAG, "Mobile.start failed", t)
        }
    }

    private fun hasStoragePermission(): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            true
        }
    }

    private fun resolveDeviceName(): String {
        val sources = listOf(
            { Settings.Global.getString(contentResolver, "device_name") },
            { Settings.Secure.getString(contentResolver, "bluetooth_name") },
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

    override fun onBind(intent: Intent?): IBinder? = null

    // Called when the user swipes WeSync out of the recents list. On
    // vanilla Android, MainActivity.onPause runs first and has already
    // scheduled a shutdown — but a few OEM skins (Xiaomi, Huawei, some
    // Samsung builds) skip activity lifecycle callbacks in this path,
    // leaving the foreground service running forever. Hook it here as a
    // belt-and-braces backstop: if no shutdown is queued yet, queue one.
    override fun onTaskRemoved(rootIntent: Intent?) {
        Log.i(TAG, "task removed — ensuring shutdown is scheduled")
        if (!main.hasCallbacks(shutdownRunnable)) {
            main.postDelayed(shutdownRunnable, GRACE_MS)
        }
        super.onTaskRemoved(rootIntent)
    }

    // OnSyncActive is pushed by the Go gate from its reconcile goroutine
    // whenever the desired ST run-state flips. active=true → ST is (about
    // to be) running, grab the radio + CPU; active=false → ST stopped,
    // let them go. Idempotent and thread-safe — the gate dedupes edges but
    // we guard anyway since it's called off the main thread.
    override fun onSyncActive(active: Boolean) {
        synchronized(lockMutex) {
            if (active) {
                acquireSyncLocks()
            } else {
                releaseSyncLocks()
            }
        }
    }

    private fun acquireSyncLocks() {
        if (multicastLock == null) {
            try {
                val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
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
                val pm = getSystemService(POWER_SERVICE) as PowerManager
                wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "wesync:sync").apply {
                    setReferenceCounted(false)
                    // Safety net: the gate caps a session at 60 min, so a
                    // 65-min timeout guarantees we never leak the lock even
                    // if a release callback is somehow missed.
                    acquire(65L * 60 * 1000)
                }
            } catch (t: Throwable) {
                Log.w(TAG, "WakeLock acquire failed", t)
            }
        }
        Log.i(TAG, "sync locks held")
    }

    private fun releaseSyncLocks() {
        multicastLock?.let { if (it.isHeld) try { it.release() } catch (_: Throwable) {} }
        multicastLock = null
        wakeLock?.let { if (it.isHeld) try { it.release() } catch (_: Throwable) {} }
        wakeLock = null
        Log.i(TAG, "sync locks released")
    }

    override fun onDestroy() {
        Log.i(TAG, "service stopping — tearing down backend")
        main.removeCallbacks(shutdownRunnable)
        try {
            Mobile.setPowerHost(null)
        } catch (_: Throwable) {
        }
        synchronized(lockMutex) { releaseSyncLocks() }
        power?.stop()
        power = null
        // backend.Stop also stops the bundled Syncthing subprocess, so by
        // the time onDestroy returns the Go side has wound down. After
        // this returns Android may reclaim the process at will.
        try {
            Mobile.stop()
        } catch (t: Throwable) {
            Log.w(TAG, "Mobile.stop failed", t)
        }
        super.onDestroy()
    }

    private fun ensureNotificationChannel() {
        val nm = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        if (nm.getNotificationChannel(CHANNEL_ID) != null) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            "WeSync running",
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = "Shown only while WeSync is actively syncing or open"
            setShowBadge(false)
        }
        nm.createNotificationChannel(channel)
    }

    private fun buildNotification(): Notification {
        val tapIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("WeSync")
            .setContentText("Running in the background")
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .setContentIntent(tapIntent)
            .build()
    }

    companion object {
        private const val TAG = "WeSync.Service"
        private const val CHANNEL_ID = "wesync-sync"
        private const val NOTIFICATION_ID = 1001

        // How long after the user leaves the UI before we let the
        // process die. Short enough that ongoing battery cost is small;
        // long enough that "swiped away by accident" doesn't pay a full
        // cold-start when returning.
        const val GRACE_MS = 5L * 60 * 1000  // 5 minutes
        const val WINDOW_GRACE_MS = 30L * 1000 // re-check every 30s once a window is active

        const val ACTION_SCHEDULE_SHUTDOWN = "com.wesync.app.SCHEDULE_SHUTDOWN"
        const val ACTION_CANCEL_SHUTDOWN = "com.wesync.app.CANCEL_SHUTDOWN"
        const val ACTION_BOOT = "com.wesync.app.BOOT"

        // Re-read the WiFi SSID by re-registering the network callback. Sent
        // by MainActivity after location is granted (and on resume while it's
        // held) — a permission change fires no network event, so without this
        // the gate would keep a stale/blank SSID until an app restart.
        const val ACTION_REFRESH_NETWORK = "com.wesync.app.REFRESH_NETWORK"
        // Sent by MainActivity after permissions are granted so the service
        // re-runs startBackendIfReady() in its onStartCommand. Using a named
        // action avoids logging a duplicate reason=initial on cold start.
        const val ACTION_START_BACKEND = "com.wesync.app.START_BACKEND"
        const val EXTRA_DELAY_MS = "delayMs"
    }
}
