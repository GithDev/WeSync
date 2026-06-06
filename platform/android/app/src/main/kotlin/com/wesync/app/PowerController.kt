package com.wesync.app

import android.Manifest
import android.annotation.SuppressLint
import android.app.AlarmManager
import android.app.PendingIntent
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
import org.json.JSONObject
import java.util.Calendar
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

// PowerController is the Android-side executor of the power gate. It owns
// NO policy: the Go gate decides everything and hands us a "wake plan"
// (Mobile.wakePlanJSON) describing exactly what to schedule. We:
//   1. Push connectivity changes to Go (Mobile.onNetworkState).
//   2. Push battery-low and charging changes to Go (Mobile.onBatteryLow /
//      onChargingState).
//   3. Read the wake plan and arm AlarmManager, whose fire calls
//      Mobile.onTriggerAlarm(). periodic/scheduled fire at their interval/times;
//      on_change arms the same alarm as a BACKSTOP tick behind its live file
//      watcher (the gate decides whether that tick actually syncs).
//
// We never interpret the trigger mode's meaning ourselves — that logic
// lives in exactly one place, the Go gate. One instance per Service
// lifetime; created in onCreate, torn down in onDestroy.
class PowerController(private val ctx: Context) {

    private val connectivityManager =
        ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
    private val alarmManager = ctx.getSystemService(Context.ALARM_SERVICE) as AlarmManager

    // Persisted across process restarts: the last SSID we positively read.
    // Used to coast through a blank background read (see currentNetworkState).
    private val prefs = ctx.getSharedPreferences("wesync_power", Context.MODE_PRIVATE)
    private var lastGoodSsid: String? = prefs.getString("last_good_ssid", null)

    // SSID from the location-info network callback. On Android 12+ this is the
    // ONLY source of an unredacted SSID — getNetworkCapabilities() always
    // strips it. Updated whenever the callback delivers wifi capabilities.
    private var ssidFromCallback: String? = null

    private var registeredLowBatteryReceiver = false
    private var registeredChargingReceiver = false
    private var networkCallback: ConnectivityManager.NetworkCallback? = null

    // Serialises every reapply() onto one thread so two of them can't race
    // while re-reading settings and re-arming AlarmManager.
    private val reapplyExecutor = Executors.newSingleThreadExecutor { r ->
        Thread(r, "wesync-power-reapply").apply { isDaemon = true }
    }

    fun start() {
        registerLowBatteryReceiver()
        registerChargingReceiver()
        registerNetworkCallback()
        // Push the initial low-battery + charging state so the gate doesn't
        // assume "off" if either actually started active.
        Mobile.onBatteryLow(isBatteryLow())
        Mobile.onChargingState(isCharging())
        reapply()
    }

    fun stop() {
        if (registeredLowBatteryReceiver) {
            try {
                ctx.unregisterReceiver(lowBatteryReceiver)
            } catch (_: Throwable) {
            }
            registeredLowBatteryReceiver = false
        }
        if (registeredChargingReceiver) {
            try {
                ctx.unregisterReceiver(chargingReceiver)
            } catch (_: Throwable) {
            }
            registeredChargingReceiver = false
        }
        networkCallback?.let {
            try {
                connectivityManager.unregisterNetworkCallback(it)
            } catch (_: Throwable) {
            }
        }
        networkCallback = null
        cancelAlarms()
        reapplyExecutor.shutdownNow()
    }

    /// Reload the gate's settings from the DB, pull the resulting wake plan,
    /// and arm AlarmManager to match.
    fun reapply() {
        reapplyExecutor.submit { reapplyBlocking() }
    }

    private fun reapplyBlocking() {
        // The backend may still be coming up (Mobile.start runs in a
        // goroutine and the bundled Syncthing's first-launch takes a few
        // seconds). Retry until the gate answers with a real plan (a non-
        // empty mode) — silently failing here was why scheduled triggers
        // never fired on a fresh install.
        var plan: WakePlan? = null
        val deadlineMs = System.currentTimeMillis() + 60_000
        var delayMs = 500L
        while (System.currentTimeMillis() < deadlineMs) {
            // Refresh BEFORE reading the plan so settings the user just
            // changed are reflected (both calls hit the in-process gate).
            try {
                Mobile.refreshPowerSettings()
            } catch (_: Throwable) {
            }
            plan = fetchWakePlan()
            if (plan != null && plan.mode.isNotEmpty()) break
            plan = null
            Thread.sleep(delayMs)
            delayMs = (delayMs * 2).coerceAtMost(5_000)
        }
        if (plan == null) {
            Log.w(TAG, "reapply gave up — gate never produced a wake plan")
            try {
                Mobile.logPowerEvent("error", "could not load wake plan — alarms not armed")
            } catch (_: Throwable) {
            }
            return
        }
        rearmAlarms(plan)
        try {
            Mobile.logPowerEvent("rearm", "alarms armed for mode=${plan.mode}")
        } catch (_: Throwable) {
        }
    }

    private fun fetchWakePlan(): WakePlan? {
        return try {
            WakePlan.fromJson(JSONObject(Mobile.wakePlanJSON()))
        } catch (t: Throwable) {
            Log.w(TAG, "fetchWakePlan: ${t.message}")
            null
        }
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
    // threshold (below). Transitions afterwards come from the precise
    // broadcasts above, which fire at that exact level.
    private fun isBatteryLow(): Boolean {
        val intent = ctx.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED)) ?: return false
        val level = intent.getIntExtra(android.os.BatteryManager.EXTRA_LEVEL, -1)
        val scale = intent.getIntExtra(android.os.BatteryManager.EXTRA_SCALE, -1)
        if (level < 0 || scale <= 0) return false
        return level * 100 / scale <= lowBatteryWarningLevel()
    }

    // The device's own low-battery warning level — the same framework value
    // that fires ACTION_BATTERY_LOW and shows the red battery icon. Read it
    // from the platform resource so we match exactly what this ROM uses,
    // rather than guessing a number. Falls back to 15 only if the framework
    // resource can't be resolved (rare/odd ROMs).
    private fun lowBatteryWarningLevel(): Int {
        return try {
            val res = android.content.res.Resources.getSystem()
            val id = res.getIdentifier("config_lowBatteryWarningLevel", "integer", "android")
            if (id != 0) res.getInteger(id) else 15
        } catch (_: Throwable) {
            15
        }
    }

    // ── Charging ──────────────────────────────────────────────────────────

    private val chargingReceiver = object : android.content.BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            Mobile.onChargingState(isCharging())
        }
    }

    private fun registerChargingReceiver() {
        val f = IntentFilter().apply {
            addAction(Intent.ACTION_POWER_CONNECTED)
            addAction(Intent.ACTION_POWER_DISCONNECTED)
        }
        ctx.registerReceiver(chargingReceiver, f)
        registeredChargingReceiver = true
    }

    // "Charging" here means ON EXTERNAL POWER, read from the battery STATUS — NOT
    // EXTRA_PLUGGED. EXTRA_PLUGGED is unreliable: on some ROMs it reads non-zero
    // with nothing connected, and since keepSyncingWhileCharging once defaulted ON
    // that phantom pinned ST awake forever (the "fresh install never sleeps" bug).
    // The battery status is the canonical signal — same as Syncthing-Android's
    // RunConditionMonitor.isOnAcPower(): CHARGING or FULL means on power (FULL
    // covers a 100% battery that's still plugged in, the case that wrongly ruled
    // out BatteryManager.isCharging() before), while a truly unplugged device
    // reports DISCHARGING. Known edge we accept (matching upstream): some devices
    // report NOT_CHARGING for wireless / dock / "optimised charging paused at
    // 80%", so those won't count as on-power — the safe direction (under-detect
    // rather than the phantom that never sleeps).
    private fun isCharging(): Boolean {
        val intent = ctx.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
            ?: return false
        val status = intent.getIntExtra(
            android.os.BatteryManager.EXTRA_STATUS,
            android.os.BatteryManager.BATTERY_STATUS_UNKNOWN,
        )
        return status == android.os.BatteryManager.BATTERY_STATUS_CHARGING ||
            status == android.os.BatteryManager.BATTERY_STATUS_FULL
    }

    // ── Network state ─────────────────────────────────────────────────────

    // Re-register the network callback so Android immediately redelivers the
    // current network's capabilities. On Android 12+ the (unredacted) SSID is
    // only obtainable through the FLAG_INCLUDE_LOCATION_INFO callback, and that
    // callback only fires on real network transitions — granting location while
    // already connected to the same WiFi fires nothing, so the SSID would stay
    // unread until a reconnect or an app restart. Forcing a re-register makes
    // the now-readable SSID land at once. No-op without location permission
    // (there's nothing readable to refresh), so it's safe to call on every
    // resume — a fresh read only happens for users who've actually granted it.
    fun refreshNetwork() {
        if (!hasLocationPermission()) return
        networkCallback?.let {
            try {
                connectivityManager.unregisterNetworkCallback(it)
            } catch (_: Throwable) {
            }
        }
        networkCallback = null
        registerNetworkCallback()
    }

    @SuppressLint("MissingPermission")
    private fun registerNetworkCallback() {
        // Idempotent: never stack a second callback. start() and a racing
        // refreshNetwork() (or refreshNetwork before start() on a cold launch)
        // could otherwise both register, leaking the first. refreshNetwork
        // nulls the field before re-registering, so it still gets its re-read.
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
    }

    // On Android 12+ (S) we MUST register with FLAG_INCLUDE_LOCATION_INFO or
    // the SSID is redacted out of the capabilities the callback delivers —
    // even with full location permission. That redaction was the whole reason
    // the gate kept seeing a blank SSID. Below S the flagged constructor
    // doesn't exist, so we use the plain one.
    @SuppressLint("MissingPermission")
    private fun newNetworkCallback(): ConnectivityManager.NetworkCallback {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            object : ConnectivityManager.NetworkCallback(
                ConnectivityManager.NetworkCallback.FLAG_INCLUDE_LOCATION_INFO,
            ) {
                override fun onAvailable(network: Network) = onNet(null)
                override fun onLost(network: Network) = onNet(null)
                override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) =
                    onNet(capabilities)
            }
        } else {
            object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) = onNet(null)
                override fun onLost(network: Network) = onNet(null)
                override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) =
                    onNet(capabilities)
            }
        }
    }

    // Single funnel for every network event. When the delivered capabilities
    // are for wifi, we grab the (now unredacted) SSID from them — this is the
    // only reliable read path on Android 12+. Then we recompute + push state.
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

    @SuppressLint("MissingPermission")
    private fun currentNetworkState(): NetState {
        var hasWifi = false
        var hasMobile = false

        val networks = connectivityManager.allNetworks
        for (n in networks) {
            val caps = connectivityManager.getNetworkCapabilities(n) ?: continue
            if (!caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) continue
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) hasWifi = true
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) hasMobile = true
        }
        // SSID comes from the location-info callback (the only unredacted
        // source on Android 12+); getNetworkCapabilities above always redacts
        // it. Empty when off wifi or before the first delivery.
        var ssid = if (hasWifi) (ssidFromCallback ?: "") else ""
        if (hasWifi) {
            if (ssid.isNotEmpty()) {
                rememberSsid(ssid)
            } else if (hasBackgroundLocationPermission()) {
                // The read failed this cycle — common right after a background
                // wifi reconnect, and flaky on some OEMs. Since we hold
                // background location (so reads are normally reliable), a blank
                // result is transient noise, not evidence of a different
                // network — coast on the last SSID we positively identified
                // rather than dropping a trusted connection. Gated on
                // background-location so a "while using"-only grant can't
                // silently coast a sync onto an untrusted LAN.
                ssid = lastGoodSsid ?: ""
            }
        }
        // Metered/roaming/activeWifi all describe the ACTIVE connection (what ST
        // would actually use), not just "some network of this type exists".
        // metered: Android's data-capped flag — true for ALL cellular plus any
        // WiFi the user (or a hotspot) marked metered. roaming: active cellular
        // on a foreign carrier (NOT_ROAMING absent). activeWifi: the active
        // network's transport is WiFi — the gate needs this to tell a metered
        // WiFi hotspot (which we skip) apart from ordinary metered cellular
        // (the user's normal data plan, which we allow).
        val metered = try {
            connectivityManager.isActiveNetworkMetered
        } catch (_: Throwable) {
            false
        }
        // getActiveNetwork() is API 23+ and NET_CAPABILITY_NOT_ROAMING is API
        // 28+. On anything older we leave activeWifi/roaming false — the same
        // graceful degradation the roaming read always had (a metered WiFi just
        // won't be distinguished from cellular there).
        var activeWifi = false
        var roaming = false
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            val activeCaps = connectivityManager.activeNetwork?.let {
                connectivityManager.getNetworkCapabilities(it)
            }
            if (activeCaps != null) {
                activeWifi = activeCaps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P &&
                    activeCaps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) {
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

    // Reads the SSID from capabilities delivered by the network callback.
    // Requires ACCESS_FINE_LOCATION; on Android 12+ it's only unredacted
    // because newNetworkCallback registered with FLAG_INCLUDE_LOCATION_INFO.
    @SuppressLint("MissingPermission")
    private fun readSsidFromCaps(caps: NetworkCapabilities): String? {
        if (!hasLocationPermission()) return null
        // API 29+ exposes WifiInfo via transportInfo (unredacted here thanks to
        // the location-info flag on 12+; not redacted at all on 10–11). Older
        // devices fall back to the deprecated WifiManager.
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

    // ── AlarmManager (sync triggers) ──────────────────────────────────────

    private fun cancelAlarms() {
        alarmManager.cancel(triggerPendingIntent())
    }

    private fun triggerPendingIntent(): PendingIntent {
        val intent = Intent(ctx, TriggerReceiver::class.java).setAction(TriggerReceiver.ACTION_FIRE)
        return PendingIntent.getBroadcast(
            ctx,
            PI_REQUEST_TRIGGER,
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    private fun rearmAlarms(p: WakePlan) {
        cancelAlarms()
        when (p.mode) {
            "periodic", "on_change" -> {
                // periodic: this is the only trigger. on_change: this is the
                // BACKSTOP tick behind the live file watcher — it catches changes
                // the watcher missed and retries a backup whose peer was offline.
                // Either way it's the same doze-friendly self-rearming alarm
                // (setAndAllowWhileIdle + RTC_WAKEUP; plain setInexactRepeating
                // never fires during doze). The gate (Mobile.onTriggerAlarm)
                // decides whether the on_change tick actually opens a session, so
                // an all-send-only device with nothing pending stays asleep.
                val ms = TimeUnit.MINUTES.toMillis(p.periodicMinutes.coerceAtLeast(1).toLong())
                val fireAt = System.currentTimeMillis() + ms
                alarmManager.setAndAllowWhileIdle(
                    AlarmManager.RTC_WAKEUP,
                    fireAt,
                    triggerPendingIntent(),
                )
                Log.i(TAG, "armed ${p.mode} alarm for ${java.util.Date(fireAt)} (every ${p.periodicMinutes} min, self-rearming)")
            }
            "scheduled" -> {
                val next = nextScheduledMillis(p.scheduledTimes)
                if (next != null) {
                    alarmManager.setAndAllowWhileIdle(
                        AlarmManager.RTC_WAKEUP,
                        next,
                        triggerPendingIntent(),
                    )
                    Log.i(TAG, "armed scheduled alarm for ${java.util.Date(next)}")
                }
            }
            else -> {
                // Unknown / empty mode (gate not ready yet) → arm nothing; the
                // next reapply will set it up once the gate answers.
            }
        }
    }

    // Next scheduled HH:MM occurrence. Pure logic lives in PowerLogic so it
    // can be unit-tested with an injected clock.
    private fun nextScheduledMillis(times: List<String>): Long? =
        PowerLogic.nextScheduledMillis(times, Calendar.getInstance())

    companion object {
        private const val TAG = "WeSync.PowerCtl"
        private const val PI_REQUEST_TRIGGER = 1100
    }
}
