package com.wesync.app

import org.json.JSONArray
import org.json.JSONObject
import java.util.Calendar

// PowerLogic holds the *decidable* bits of the Android power wrapper — the
// parsing and date math that used to be buried inside PowerController where it
// could only be checked by running the app on a phone.
//
// Everything here is pure: java.util + org.json + kotlin stdlib, no android.*
// imports, no system services, clock injected as a parameter. That makes it
// unit-testable on the plain JVM (see PowerLogicTest), which is the whole
// point — this is the logic that was "guessed at" before.
//
// The *policy* (when ST may run, when a session ends) lives in the Go gate and
// is tested there; this is only the mechanical translation of the gate's wake
// plan into alarms.
object PowerLogic {

    // nextScheduledMillis finds the soonest future occurrence of any HH:MM
    // in `times`, relative to `now`. Returns null if the list is empty or
    // every entry is malformed. `now` is injected so the day-rollover and
    // "already passed today → tomorrow" branches are deterministic in tests.
    //
    // Malformed entries (wrong shape, non-numeric, hour > 23, minute > 59)
    // are skipped, not clamped — a typo in settings must never silently
    // schedule a sync at the wrong time.
    fun nextScheduledMillis(times: List<String>, now: Calendar): Long? {
        var best: Long? = null
        for (t in times) {
            val parts = t.split(":")
            if (parts.size != 2) continue
            val h = parts[0].toIntOrNull() ?: continue
            val m = parts[1].toIntOrNull() ?: continue
            if (h !in 0..23 || m !in 0..59) continue
            val cand = (now.clone() as Calendar).apply {
                set(Calendar.HOUR_OF_DAY, h)
                set(Calendar.MINUTE, m)
                set(Calendar.SECOND, 0)
                set(Calendar.MILLISECOND, 0)
            }
            if (!cand.after(now)) {
                cand.add(Calendar.DAY_OF_MONTH, 1)
            }
            val ms = cand.timeInMillis
            if (best == null || ms < best) best = ms
        }
        return best
    }

    // cleanSsid normalises what WifiManager hands us. WifiInfo wraps UTF-8
    // SSIDs in double quotes; "<unknown ssid>" is what comes back when the
    // location permission is missing. Both the empty and unknown cases map
    // to null so the gate's trusted_wifi mode can refuse to open rather
    // than match against garbage.
    fun cleanSsid(raw: String?): String? {
        if (raw == null) return null
        val trimmed = raw.trim('"')
        if (trimmed.isEmpty() || trimmed == "<unknown ssid>") return null
        return trimmed
    }
}

// WakePlan is the gate's instruction to the Android wrapper: what to
// schedule, nothing more. Mirrors the JSON from Mobile.wakePlanJSON(). The
// gate owns the trigger-mode interpretation; this is purely mechanical.
data class WakePlan(
    val mode: String,
    val periodicMinutes: Int,
    val scheduledTimes: List<String>,
) {
    companion object {
        fun fromJson(o: JSONObject): WakePlan {
            return WakePlan(
                mode = o.optString("mode", ""),
                periodicMinutes = o.optInt("periodicMinutes", 120),
                scheduledTimes = toStringList(o.optJSONArray("scheduledTimes")),
            )
        }

        private fun toStringList(a: JSONArray?): List<String> {
            if (a == null) return emptyList()
            val out = mutableListOf<String>()
            for (i in 0 until a.length()) {
                val v = a.optString(i)
                if (v.isNotEmpty()) out.add(v)
            }
            return out
        }
    }
}
