package com.wesync.app

import android.content.Context
import android.util.Log
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import kotlinx.coroutines.delay
import mobile.Mobile
import org.json.JSONObject
import java.util.Calendar
import java.util.concurrent.TimeUnit

// SyncScheduler is the Android-side executor of the gate's "wake plan": it reads
// Mobile.wakePlanJSON() and enqueues WorkManager requests. It owns NO policy —
// the Go gate decides everything (which mode, intervals, scheduled times). This
// replaces the old AlarmManager arming, which relied on a background
// startForegroundService that Android 12+ silently rejected.
//
// WorkManager handles Doze batching and reboot persistence itself, so there is
// no boot receiver and no self-rearming alarm chain. The actual sync runs in
// SyncWorker (a long-running foreground worker that holds the process alive
// until the gate reports the sync finished).
object SyncScheduler {
    private const val TAG = "WeSync.Scheduler"

    private const val WORK_PERIODIC = "wesync-sync-periodic"
    private const val WORK_POLL = "wesync-sync-poll"
    private const val WORK_SAFETY = "wesync-sync-safety"
    private const val WORK_SCHEDULED = "wesync-sync-scheduled"
    private const val WORK_REAPPLY = "wesync-sync-reapply"

    // Re-read settings + wake plan and (re)arm the schedule. Runs as a one-shot
    // worker so it survives the caller (activity/service) and runs off the main
    // thread; it waits for the gate to be ready (fresh-install / cold start).
    // Call on app open and after the user changes power settings.
    fun reapply(ctx: Context) {
        val req = OneTimeWorkRequestBuilder<ReapplyScheduleWorker>().build()
        WorkManager.getInstance(ctx).enqueueUniqueWork(WORK_REAPPLY, ExistingWorkPolicy.REPLACE, req)
    }

    // Cancel every sync wake-up (used when tearing down / debugging).
    fun cancelAll(ctx: Context) {
        val wm = WorkManager.getInstance(ctx)
        wm.cancelUniqueWork(WORK_PERIODIC)
        wm.cancelUniqueWork(WORK_POLL)
        wm.cancelUniqueWork(WORK_SAFETY)
        wm.cancelUniqueWork(WORK_SCHEDULED)
    }

    // Re-arm only the next scheduled-time occurrence. Called by SyncWorker after
    // a scheduled run completes, the WorkManager analogue of the old alarm
    // self-rearm.
    fun enqueueNextScheduled(ctx: Context) {
        val plan = readPlanOrNull() ?: return
        if (plan.mode != "scheduled") return
        enqueueScheduled(ctx, plan.scheduledTimes)
    }

    // Read the gate's wake plan; null if the gate isn't ready (empty mode) or on
    // any parse error.
    fun readPlanOrNull(): WakePlan? {
        return try {
            val p = WakePlan.fromJson(JSONObject(Mobile.wakePlanJSON()))
            if (p.mode.isEmpty()) null else p
        } catch (t: Throwable) {
            Log.w(TAG, "readPlan failed: ${t.message}")
            null
        }
    }

    // Translate a wake plan into WorkManager requests. Cancels the modes we're
    // not using so a mode switch never leaves stale work behind.
    fun enqueueFromPlan(ctx: Context, plan: WakePlan) {
        val wm = WorkManager.getInstance(ctx)
        when (plan.mode) {
            "periodic" -> {
                wm.cancelUniqueWork(WORK_POLL)
                wm.cancelUniqueWork(WORK_SAFETY)
                wm.cancelUniqueWork(WORK_SCHEDULED)
                enqueuePeriodic(wm, WORK_PERIODIC, plan.periodicMinutes, SyncWorker.ROLE_TRIGGER)
            }
            "on_change_poll" -> {
                // Two independent periodics: a poll (opens a session only if a
                // structural change is detected) and a safety net (always syncs,
                // to receive peer changes).
                wm.cancelUniqueWork(WORK_PERIODIC)
                wm.cancelUniqueWork(WORK_SCHEDULED)
                enqueuePeriodic(wm, WORK_POLL, plan.onChangePollMinutes, SyncWorker.ROLE_POLL)
                enqueuePeriodic(wm, WORK_SAFETY, plan.periodicMinutes, SyncWorker.ROLE_TRIGGER)
            }
            "scheduled" -> {
                wm.cancelUniqueWork(WORK_PERIODIC)
                wm.cancelUniqueWork(WORK_POLL)
                wm.cancelUniqueWork(WORK_SAFETY)
                enqueueScheduled(ctx, plan.scheduledTimes)
            }
            else -> Log.w(TAG, "unknown wake-plan mode '${plan.mode}' — leaving schedule unchanged")
        }
    }

    private fun enqueuePeriodic(wm: WorkManager, name: String, minutes: Int, role: String) {
        val interval = PowerLogic.clampWakeIntervalMinutes(minutes)
        val req = PeriodicWorkRequestBuilder<SyncWorker>(interval, TimeUnit.MINUTES)
            .setInputData(workDataOf(SyncWorker.KEY_ROLE to role))
            .build()
        wm.enqueueUniquePeriodicWork(name, ExistingPeriodicWorkPolicy.UPDATE, req)
        Log.i(TAG, "armed $name every $interval min (role=$role)")
    }

    private fun enqueueScheduled(ctx: Context, times: List<String>) {
        val wm = WorkManager.getInstance(ctx)
        val next = PowerLogic.nextScheduledMillis(times, Calendar.getInstance())
        if (next == null) {
            wm.cancelUniqueWork(WORK_SCHEDULED)
            Log.i(TAG, "no valid scheduled times — scheduled work cancelled")
            return
        }
        val delayMs = (next - System.currentTimeMillis()).coerceAtLeast(0)
        val req = OneTimeWorkRequestBuilder<SyncWorker>()
            .setInitialDelay(delayMs, TimeUnit.MILLISECONDS)
            .setInputData(workDataOf(SyncWorker.KEY_ROLE to SyncWorker.ROLE_SCHEDULED))
            .build()
        wm.enqueueUniqueWork(WORK_SCHEDULED, ExistingWorkPolicy.REPLACE, req)
        Log.i(TAG, "armed scheduled work for ${java.util.Date(next)}")
    }
}

// One-shot worker that (re)reads the gate's settings + wake plan and arms the
// WorkManager schedule. Retries until the gate answers with a real plan — on a
// fresh install / cold start the backend comes up asynchronously, so the plan
// isn't ready immediately. Best-effort: relies on the backend already running
// (it's enqueued from the UI service / a settings change, both with the backend
// up); if the gate never answers it logs and gives up until the next reapply.
class ReapplyScheduleWorker(
    ctx: Context,
    params: WorkerParameters,
) : CoroutineWorker(ctx, params) {
    override suspend fun doWork(): Result {
        val deadlineMs = System.currentTimeMillis() + 60_000
        var delayMs = 500L
        var plan: WakePlan? = null
        while (System.currentTimeMillis() < deadlineMs) {
            try {
                Mobile.refreshPowerSettings()
            } catch (_: Throwable) {
            }
            plan = SyncScheduler.readPlanOrNull()
            if (plan != null) break
            delay(delayMs)
            delayMs = (delayMs * 2).coerceAtMost(5_000)
        }
        val p = plan
        if (p == null) {
            try {
                Mobile.logPowerEvent("error", "could not load wake plan — schedule not armed")
            } catch (_: Throwable) {
            }
            return Result.success()
        }
        SyncScheduler.enqueueFromPlan(applicationContext, p)
        try {
            Mobile.logPowerEvent("rearm", "schedule armed for mode=${p.mode}")
        } catch (_: Throwable) {
        }
        return Result.success()
    }
}
