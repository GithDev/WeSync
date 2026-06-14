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

    // Sync work names live in PowerLogic (single source for planWorks). This is
    // the one-shot re-apply worker, separate from the scheduled sync work.
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
        PowerLogic.ALL_SYNC_WORK.forEach { wm.cancelUniqueWork(it) }
    }

    // Re-arm the next scheduled-time occurrence. Called by SyncWorker after a
    // scheduled run completes, the WorkManager analogue of the old alarm
    // self-rearm. Routes through enqueueFromPlan so the decision stays in one
    // place (planWorks).
    fun enqueueNextScheduled(ctx: Context) {
        val plan = readPlanOrNull() ?: return
        if (plan.mode != "scheduled") return
        enqueueFromPlan(ctx, plan)
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

    // Translate a wake plan into WorkManager requests. The DECISION (which jobs,
    // which intervals) lives in PowerLogic.planWorks — here we only enqueue what
    // it returns and cancel every other sync work, so a mode switch never leaves
    // stale work behind.
    fun enqueueFromPlan(ctx: Context, plan: WakePlan) {
        val wm = WorkManager.getInstance(ctx)
        val planned = PowerLogic.planWorks(plan, Calendar.getInstance())
        val active = planned.map { it.workName }.toSet()
        PowerLogic.ALL_SYNC_WORK.forEach { if (it !in active) wm.cancelUniqueWork(it) }
        if (planned.isEmpty()) {
            Log.w(TAG, "wake plan '${plan.mode}' yielded no work — nothing scheduled")
            return
        }
        planned.forEach { enqueue(wm, it) }
    }

    private fun enqueue(wm: WorkManager, p: PlannedWork) {
        val data = workDataOf(SyncWorker.KEY_ROLE to p.role)
        if (p.periodicMinutes > 0) {
            val req = PeriodicWorkRequestBuilder<SyncWorker>(p.periodicMinutes, TimeUnit.MINUTES)
                .setInputData(data)
                .build()
            wm.enqueueUniquePeriodicWork(p.workName, ExistingPeriodicWorkPolicy.UPDATE, req)
            Log.i(TAG, "armed ${p.workName} every ${p.periodicMinutes} min (role=${p.role})")
        } else {
            val req = OneTimeWorkRequestBuilder<SyncWorker>()
                .setInitialDelay(p.oneTimeDelayMs, TimeUnit.MILLISECONDS)
                .setInputData(data)
                .build()
            wm.enqueueUniqueWork(p.workName, ExistingWorkPolicy.REPLACE, req)
            Log.i(TAG, "armed ${p.workName} (one-time +${p.oneTimeDelayMs}ms, role=${p.role})")
        }
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
