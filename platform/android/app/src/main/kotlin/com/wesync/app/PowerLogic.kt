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

    // WorkManager's hard floor for periodic work is 15 minutes, and under Doze
    // no mechanism wakes more often than that anyway — so any shorter requested
    // interval (the gate's onChangePollMinutes can be smaller) is clamped up.
    // Lives here so the floor that defines the rebuild's scheduling contract is
    // unit-pinned on the plain JVM, away from the androidx.work-coupled caller.
    const val MIN_WAKE_INTERVAL_MIN = 15L

    fun clampWakeIntervalMinutes(requestedMinutes: Int): Long =
        maxOf(MIN_WAKE_INTERVAL_MIN, requestedMinutes.toLong())

    // Unique WorkManager work names (single source — SyncScheduler enqueues
    // exactly what planWorks() returns and cancels the rest).
    const val WORK_PERIODIC = "wesync-sync-periodic"
    const val WORK_POLL = "wesync-sync-poll"
    const val WORK_SAFETY = "wesync-sync-safety"
    const val WORK_SCHEDULED = "wesync-sync-scheduled"
    val ALL_SYNC_WORK = listOf(WORK_PERIODIC, WORK_POLL, WORK_SAFETY, WORK_SCHEDULED)

    // Worker roles — the input-data value SyncWorker reads to pick its trigger.
    const val ROLE_TRIGGER = "trigger"     // always opens a session (periodic / on_change_poll safety net)
    const val ROLE_POLL = "poll"           // on_change_poll fast path — sync only if changed
    const val ROLE_SCHEDULED = "scheduled" // a scheduled-time trigger; the worker re-arms the next one

    // planWorks reduces a wake plan to the EXACT set of WorkManager jobs that
    // should be active. Pure (clock injected) so the scheduling decision is
    // unit-testable; SyncScheduler just enqueues what this returns and cancels
    // every other ALL_SYNC_WORK entry. Intervals are clamped to the 15-min floor
    // here. Empty list = schedule nothing (unknown/empty mode, or scheduled mode
    // with no valid times).
    fun planWorks(plan: WakePlan, now: Calendar): List<PlannedWork> = when (plan.mode) {
        "periodic" -> listOf(
            PlannedWork(WORK_PERIODIC, ROLE_TRIGGER, periodicMinutes = clampWakeIntervalMinutes(plan.periodicMinutes)),
        )
        "on_change_poll" -> listOf(
            PlannedWork(WORK_POLL, ROLE_POLL, periodicMinutes = clampWakeIntervalMinutes(plan.onChangePollMinutes)),
            PlannedWork(WORK_SAFETY, ROLE_TRIGGER, periodicMinutes = clampWakeIntervalMinutes(plan.periodicMinutes)),
        )
        "scheduled" -> {
            val next = nextScheduledMillis(plan.scheduledTimes, now)
            if (next == null) {
                emptyList()
            } else {
                listOf(PlannedWork(WORK_SCHEDULED, ROLE_SCHEDULED, oneTimeDelayMs = (next - now.timeInMillis).coerceAtLeast(0)))
            }
        }
        else -> emptyList()
    }
}

// PlannedWork is one WorkManager job the wake plan implies. periodicMinutes > 0
// means a periodic request at that interval; otherwise it's a one-time request
// fired after oneTimeDelayMs (the scheduled-time case).
data class PlannedWork(
    val workName: String,
    val role: String,
    val periodicMinutes: Long = 0,
    val oneTimeDelayMs: Long = 0,
)

// WakePlan is the gate's instruction to the Android wrapper: what to
// schedule, nothing more. Mirrors the JSON from Mobile.wakePlanJSON(). The
// gate owns the trigger-mode interpretation; this is purely mechanical.
data class WakePlan(
    val mode: String,
    val periodicMinutes: Int,
    val onChangePollMinutes: Int,
    val scheduledTimes: List<String>,
) {
    companion object {
        fun fromJson(o: JSONObject): WakePlan {
            return WakePlan(
                mode = o.optString("mode", ""),
                periodicMinutes = o.optInt("periodicMinutes", 120),
                onChangePollMinutes = o.optInt("onChangePollMinutes", 5),
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
