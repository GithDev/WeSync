package com.wesync.app

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.wifi.WifiInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.util.Log
import mobile.Mobile
import java.util.concurrent.ConcurrentHashMap

// PowerController feeds the Go gate LIVE network + battery state WHILE THE UI IS
// OPEN. It owns no scheduling — background wake-ups are WorkManager's job
// (SyncScheduler/SyncWorker), and a cold worker reads a one-shot snapshot via
// PowerSignals instead. This class exists for the foreground case: so the
// "Now" status panel and the trusted-WiFi gate reflect reality as the network
// changes under the user, and so a sync triggered while the app is open sees
// current state.
//
// One instance per WeSyncService lifetime; created in onCreate, torn down in
// onDestroy.
class PowerController(private val ctx: Context) {

    private val connectivityManager =
        ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    // Persisted across process restarts: the last SSID we positively read.
    // Used to coast through a blank background read, and read by PowerSignals in
    // a cold worker process (which has no live callback).
    private val prefs = ctx.getSharedPreferences("wesync_power", Context.MODE_PRIVATE)
    private var lastGoodSsid: String? = prefs.getString("last_good_ssid", null)

    // SSID from the location-info network callback. On Android 12+ this is the
    // ONLY source of an unredacted SSID — getNetworkCapabilities() always strips
    // it. Updated whenever the callback delivers wifi capabilities.
    private var ssidFromCallback: String? = null

    private var registeredLowBatteryReceiver = false
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private val liveNetworkCaps = ConcurrentHashMap<Network, NetworkCapabilities>()

    fun start() {
        registerLowBatteryReceiver()
        // registerNetworkCallback seeds liveNetworkCaps synchronously from
        // allNetworks, so currentNetworkState() is immediately accurate.
        registerNetworkCallback()
        // Push the initial network + battery state so the gate has real values.
        val net = currentNetworkState()
        try {
            Mobile.onNetworkState(net.ssid, net.hasWifi, net.hasMobile, net.metered, net.roaming, net.activeWifi)
        } catch (_: Throwable) {
        }
        try { Mobile.onBatteryLow(isBatteryLow()) } catch (_: Throwable) {}
    }

    fun stop() {
        if (registeredLowBatteryReceiver) {
            try {
                ctx.unregisterReceiver(lowBatteryReceiver)
            } catch (_: Throwable) {
            }
            registeredLowBatteryReceiver = false
        }
        networkCallback?.let {
            try {
                connectivityManager.unregisterNetworkCallback(it)
            } catch (_: Throwable) {
            }
        }
        networkCallback = null
        liveNetworkCaps.clear()
    }

    // ── Low battery ───────────────────────────────────────────────────────
    // The battery is "low" at the level where Android shows its low-battery
    // warning — ACTION_BATTERY_LOW / ACTION_BATTERY_OKAY straddle exactly that
    // threshold. This is NOT battery-saver mode (a separate user setting).

    private val lowBatteryReceiver = object : android.content.BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            when (intent?.action) {
                Intent.ACTION_BATTERY_LOW -> Mobile.onBatteryLow(true)
                Intent.ACTION_BATTERY_OKAY -> Mobile.onBatteryLow(false)
            }
        }
    }

    private fun registerLowBatteryReceiver() {
        val f = IntentFilter().apply {
            addAction(Intent.ACTION_BATTERY_LOW)
            addAction(Intent.ACTION_BATTERY_OKAY)
        }
        ctx.registerReceiver(lowBatteryReceiver, f)
        registeredLowBatteryReceiver = true
    }

    // BATTERY_LOW/OKAY are not sticky, so for the initial state we read the
    // sticky ACTION_BATTERY_CHANGED and compare against the OS's OWN warning
    // threshold. Transitions afterwards come from the precise broadcasts above.
    private fun isBatteryLow(): Boolean {
        val intent = ctx.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED)) ?: return false
        val level = intent.getIntExtra(android.os.BatteryManager.EXTRA_LEVEL, -1)
        val scale = intent.getIntExtra(android.os.BatteryManager.EXTRA_SCALE, -1)
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

    // ── Network state ─────────────────────────────────────────────────────

    // Re-register the network callback so Android immediately redelivers the
    // current network's capabilities. On Android 12+ the (unredacted) SSID is
    // only obtainable through the FLAG_INCLUDE_LOCATION_INFO callback, and that
    // callback only fires on real network transitions — granting location while
    // already connected fires nothing, so the SSID would stay unread until a
    // reconnect or app restart. Forcing a re-register makes the now-readable
    // SSID land at once. No-op without location permission.
    fun refreshNetwork() {
        if (!hasLocationPermission()) return
        networkCallback?.let {
            try {
                connectivityManager.unregisterNetworkCallback(it)
            } catch (_: Throwable) {
            }
        }
        networkCallback = null
        liveNetworkCaps.clear()
        registerNetworkCallback()
    }

    @SuppressLint("MissingPermission")
    private fun registerNetworkCallback() {
        if (networkCallback != null) return
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        val cb = newNetworkCallback()
        try {
            connectivityManager.registerNetworkCallback(request, cb)
            networkCallback = cb
        } catch (t: Throwable) {
            Log.w(TAG, "registerNetworkCallback failed", t)
        }
        // Seed liveNetworkCaps with already-known networks so currentNetworkState()
        // is accurate before any callback is delivered.
        connectivityManager.allNetworks.forEach { network ->
            connectivityManager.getNetworkCapabilities(network)?.let { caps ->
                liveNetworkCaps[network] = caps
            }
        }
    }

    // On Android 12+ (S) we MUST register with FLAG_INCLUDE_LOCATION_INFO or the
    // SSID is redacted out of the capabilities the callback delivers — even with
    // full location permission.
    @SuppressLint("MissingPermission")
    private fun newNetworkCallback(): ConnectivityManager.NetworkCallback {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            object : ConnectivityManager.NetworkCallback(
                ConnectivityManager.NetworkCallback.FLAG_INCLUDE_LOCATION_INFO,
            ) {
                override fun onAvailable(network: Network) = onNet(null)
                override fun onLost(network: Network) { liveNetworkCaps.remove(network); onNet(null) }
                override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) {
                    liveNetworkCaps[network] = capabilities; onNet(capabilities)
                }
            }
        } else {
            object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) = onNet(null)
                override fun onLost(network: Network) { liveNetworkCaps.remove(network); onNet(null) }
                override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) {
                    liveNetworkCaps[network] = capabilities; onNet(capabilities)
                }
            }
        }
    }

    // Single funnel for every network event. When the delivered capabilities are
    // for wifi, we grab the (now unredacted) SSID from them — the only reliable
    // read path on Android 12+. Then we recompute + push state.
    private fun onNet(caps: NetworkCapabilities?) {
        if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
            readSsidFromCaps(caps)?.let { ssidFromCallback = it }
        }
        val state = currentNetworkState()
        Mobile.onNetworkState(state.ssid, state.hasWifi, state.hasMobile, state.metered, state.roaming, state.activeWifi)
    }

    private data class NetState(
        val ssid: String,
        val hasWifi: Boolean,
        val hasMobile: Boolean,
        val metered: Boolean,
        val roaming: Boolean,
        val activeWifi: Boolean,
    )

    private fun currentNetworkState(): NetState {
        var hasWifi = false
        var hasMobile = false

        for (caps in liveNetworkCaps.values) {
            if (!caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) continue
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) hasWifi = true
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) hasMobile = true
        }
        var ssid = if (hasWifi) (ssidFromCallback ?: "") else ""
        if (hasWifi) {
            if (ssid.isNotEmpty()) {
                rememberSsid(ssid)
            } else if (hasBackgroundLocationPermission()) {
                // Transient blank read right after a background reconnect — coast
                // on the last SSID we positively identified rather than dropping a
                // trusted connection. Gated on background-location.
                ssid = lastGoodSsid ?: ""
            }
        }
        val metered = try {
            connectivityManager.isActiveNetworkMetered
        } catch (_: Throwable) {
            false
        }
        var activeWifi = false
        var roaming = false
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val activeCaps = connectivityManager.activeNetwork?.let {
                connectivityManager.getNetworkCapabilities(it)
            }
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

    private fun rememberSsid(ssid: String) {
        if (ssid == lastGoodSsid) return
        lastGoodSsid = ssid
        prefs.edit().putString("last_good_ssid", ssid).apply()
    }

    private fun hasBackgroundLocationPermission(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return hasLocationPermission()
        return ctx.checkSelfPermission(Manifest.permission.ACCESS_BACKGROUND_LOCATION) ==
            PackageManager.PERMISSION_GRANTED
    }

    @SuppressLint("MissingPermission")
    private fun readSsidFromCaps(caps: NetworkCapabilities): String? {
        if (!hasLocationPermission()) return null
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            val info = caps.transportInfo as? WifiInfo
            if (info != null) return PowerLogic.cleanSsid(info.ssid)
        }
        @Suppress("DEPRECATION")
        val wifi = ctx.getSystemService(Context.WIFI_SERVICE) as WifiManager
        @Suppress("DEPRECATION")
        return PowerLogic.cleanSsid(wifi.connectionInfo?.ssid)
    }

    private fun hasLocationPermission(): Boolean {
        return ctx.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) ==
            PackageManager.PERMISSION_GRANTED
    }

    companion object {
        private const val TAG = "WeSync.PowerCtl"
    }
}
