package com.wesync.app

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.content.IntentFilter
import android.net.NetworkCapabilities
import android.net.wifi.WifiManager
import android.os.Build
import android.os.BatteryManager
import android.util.Log
import mobile.Mobile

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
