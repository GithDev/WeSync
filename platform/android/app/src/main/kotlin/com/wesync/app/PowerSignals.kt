package com.wesync.app

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.content.IntentFilter
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.wifi.WifiInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.BatteryManager
import android.util.Log
import mobile.Mobile
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

// One-shot read of the device's current power-relevant state, pushed into the
// Go gate. Used by the WorkManager SyncWorker, which runs in a fresh process
// with no live network callbacks (PowerController's live path only exists while
// the UI is foreground). Without this seed a background wake-up would evaluate
// the gate against zero-value inputs and silently skip the sync.
//
// Stateless on purpose — every call re-queries the system. The one value it
// can't read cold is the unredacted WiFi SSID (that needs the location-info
// network callback, which only fires on transitions), so for trusted_wifi it
// coasts on the last SSID PowerController positively identified, persisted in
// the same SharedPreferences. Gated on background-location so a "while using"
// grant can't coast a sync onto an untrusted LAN.
object PowerSignals {
    private const val TAG = "WeSync.Signals"
    private const val PREFS = "wesync_power"
    private const val KEY_LAST_SSID = "last_good_ssid"

    // Compute the current state and push it into the gate. Charging is no longer
    // a gate input, so it isn't read or sent.
    fun pushToGate(ctx: Context) {
        val net = readNetwork(ctx)
        try {
            Mobile.onNetworkState(net.ssid, net.hasWifi, net.hasMobile, net.metered, net.roaming, net.activeWifi)
        } catch (t: Throwable) {
            Log.w(TAG, "onNetworkState push failed", t)
        }
        try {
            Mobile.onBatteryLow(isBatteryLow(ctx))
        } catch (t: Throwable) {
            Log.w(TAG, "onBatteryLow push failed", t)
        }
    }

    // Is the WiFi radio on? Distinguishes "we're home and wifi is associating
    // after the wake" (radio on) from "we're away" (radio off). The SyncWorker
    // uses this to decide whether a short network-settle grace is worth waiting
    // out, or whether to bail instantly (no battery cost when away).
    fun isWifiEnabled(ctx: Context): Boolean {
        return try {
            (ctx.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager).isWifiEnabled
        } catch (_: Throwable) {
            false
        }
    }

    // Read the CURRENT network with a LIVE SSID and push it to the gate. On
    // Android 12+ the unredacted SSID is only available through a network
    // callback registered with FLAG_INCLUDE_LOCATION_INFO — a one-shot read
    // can't see it, which is why the plain path coasts on a cached SSID. That
    // cache can be stale and let trusted_wifi sync on the WRONG network, so the
    // worker uses this instead: register the callback briefly, read the real
    // SSID, persist it, then push. It also doubles as the WiFi-settle grace —
    // it waits up to timeoutMs for WiFi to associate after the wake. Falls back
    // to the one-shot read if no WiFi/SSID is delivered (e.g. WiFi never came up
    // or only foreground location is granted).
    fun pushLiveNetwork(ctx: Context, timeoutMs: Long) {
        val app = ctx.applicationContext
        val cm = app.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val latch = CountDownLatch(1)
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        val cb = newLiveCallback(app) { latch.countDown() }
        var registered = false
        try {
            cm.registerNetworkCallback(request, cb)
            registered = true
            latch.await(timeoutMs, TimeUnit.MILLISECONDS)
        } catch (t: Throwable) {
            Log.w(TAG, "pushLiveNetwork failed", t)
        } finally {
            if (registered) {
                try { cm.unregisterNetworkCallback(cb) } catch (_: Throwable) {}
            }
        }
        // By now the live SSID (if WiFi delivered one) is persisted, so the
        // one-shot read below reflects the REAL current network.
        pushToGate(app)
    }

    @SuppressLint("MissingPermission")
    private fun newLiveCallback(ctx: Context, onWifiSsidResolved: () -> Unit): ConnectivityManager.NetworkCallback {
        val onCaps: (NetworkCapabilities) -> Unit = { caps ->
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                val ssid = liveSsid(ctx, caps)
                if (!ssid.isNullOrEmpty()) {
                    rememberSsid(ctx, ssid)
                    onWifiSsidResolved()
                }
            }
        }
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            object : ConnectivityManager.NetworkCallback(
                ConnectivityManager.NetworkCallback.FLAG_INCLUDE_LOCATION_INFO,
            ) {
                override fun onCapabilitiesChanged(n: Network, c: NetworkCapabilities) = onCaps(c)
            }
        } else {
            object : ConnectivityManager.NetworkCallback() {
                override fun onCapabilitiesChanged(n: Network, c: NetworkCapabilities) = onCaps(c)
            }
        }
    }

    @SuppressLint("MissingPermission")
    private fun liveSsid(ctx: Context, caps: NetworkCapabilities): String? {
        if (ctx.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED) {
            return null
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            val info = caps.transportInfo as? WifiInfo
            if (info != null) return PowerLogic.cleanSsid(info.ssid)
        }
        return null
    }

    private fun rememberSsid(ctx: Context, ssid: String) {
        try {
            ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_LAST_SSID, ssid).apply()
        } catch (_: Throwable) {
        }
    }

    private data class NetState(
        val ssid: String,
        val hasWifi: Boolean,
        val hasMobile: Boolean,
        val metered: Boolean,
        val roaming: Boolean,
        val activeWifi: Boolean,
    )

    private fun readNetwork(ctx: Context): NetState {
        val cm = ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        var hasWifi = false
        var hasMobile = false
        for (network in cm.allNetworks) {
            val caps = cm.getNetworkCapabilities(network) ?: continue
            if (!caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) continue
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) hasWifi = true
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) hasMobile = true
        }

        // SSID can't be read live in a cold worker process; coast on the last
        // one PowerController positively identified (background-location gated).
        var ssid = ""
        if (hasWifi && hasBackgroundLocation(ctx)) {
            ssid = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_LAST_SSID, "") ?: ""
        }

        val metered = try {
            cm.isActiveNetworkMetered
        } catch (_: Throwable) {
            false
        }
        var activeWifi = false
        var roaming = false
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val activeCaps = cm.activeNetwork?.let { cm.getNetworkCapabilities(it) }
            if (activeCaps != null) {
                activeWifi = activeCaps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P &&
                    activeCaps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)
                ) {
                    roaming = !activeCaps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_ROAMING)
                }
            }
        }
        return NetState(ssid, hasWifi, hasMobile, metered, roaming, activeWifi)
    }

    // "Low" at the level where Android shows its low-battery warning. Read from
    // the sticky ACTION_BATTERY_CHANGED and compared to the framework's own
    // warning threshold (matches PowerController.isBatteryLow).
    private fun isBatteryLow(ctx: Context): Boolean {
        val intent = ctx.registerReceiver(null, IntentFilter(android.content.Intent.ACTION_BATTERY_CHANGED))
            ?: return false
        val level = intent.getIntExtra(BatteryManager.EXTRA_LEVEL, -1)
        val scale = intent.getIntExtra(BatteryManager.EXTRA_SCALE, -1)
        if (level < 0 || scale <= 0) return false
        return level * 100 / scale <= lowBatteryWarningLevel()
    }

    private fun lowBatteryWarningLevel(): Int {
        return try {
            val res = android.content.res.Resources.getSystem()
            val id = res.getIdentifier("config_lowBatteryWarningLevel", "integer", "android")
            if (id != 0) res.getInteger(id) else 15
        } catch (_: Throwable) {
            15
        }
    }

    private fun hasBackgroundLocation(ctx: Context): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            return ctx.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) ==
                PackageManager.PERMISSION_GRANTED
        }
        return ctx.checkSelfPermission(Manifest.permission.ACCESS_BACKGROUND_LOCATION) ==
            PackageManager.PERMISSION_GRANTED
    }
}
