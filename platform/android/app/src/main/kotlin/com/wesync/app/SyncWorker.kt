package com.wesync.app

import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import android.util.Log
import androidx.work.CoroutineWorker
import androidx.work.ForegroundInfo
import androidx.work.WorkerParameters
import kotlinx.coroutines.delay
import mobile.Mobile

// SyncWorker is one background sync wake-up. It runs as a long-running
// foreground worker: it promotes itself to a foreground service (dataSync) for
// the whole sync, brings the Go backend up, seeds the current network/battery
// state, asks the gate to open a session, then HOLDS the process alive until the
// gate reports the sync has finished on its own. That last part is the
// "never interrupt a sync" guarantee — WorkManager owns the foreground-service
// start (its sanctioned path), so the background-FGS-start failure that broke
// the old AlarmManager path can't happen here.
//
// The role (which trigger to fire) is passed in the input data by SyncScheduler.
class SyncWorker(
    appContext: Context,
    params: WorkerParameters,
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): Result {
        val role = inputData.getString(KEY_ROLE) ?: PowerLogic.ROLE_TRIGGER
        try {
            setForeground(buildForegroundInfo())
        } catch (t: Throwable) {
            // Not fatal — the work can still run, just without the FGS promotion
            // (e.g. on a restrictive OEM). The gate/locks still function.
            Log.w(TAG, "setForeground failed", t)
        }

        val owner = BackendOwnership.workerOwner(id.toString())
        BackendOwnership.acquire(applicationContext, owner)
        try {
            if (!awaitBackendReady()) {
                Log.w(TAG, "backend did not come up — skipping this wake-up")
                return Result.success()
            }

            // Record the autonomous wake-up so it's visible in Recent activity —
            // this is how the user verifies background sync actually fired while
            // the app was closed (e.g. overnight, after a reboot).
            try {
                Mobile.logPowerEvent("wake", "background wake — role=$role")
            } catch (_: Throwable) {
            }

            // Seed current conditions BEFORE triggering, or the gate evaluates
            // network/battery against zero-value inputs and silently skips.
            //
            // On WiFi, read the LIVE network via a callback — get the *current*
            // SSID (never a stale cache, which could let trusted_wifi sync on the
            // wrong network) and wait up to NETWORK_SETTLE_MS for WiFi to
            // associate after the wake. If WiFi is OFF (we're away), one-shot read
            // and bail instantly — no wait, no battery cost.
            if (PowerSignals.isWifiEnabled(applicationContext)) {
                PowerSignals.pushLiveNetwork(applicationContext, NETWORK_SETTLE_MS)
            } else {
                PowerSignals.pushToGate(applicationContext)
            }

            when (role) {
                PowerLogic.ROLE_POLL -> Mobile.onTriggerPollAlarm()
                else -> Mobile.onTriggerAlarm()
            }

            // Never interrupt a sync: hold the foreground until the gate lets the
            // session lapse on its own (ST idle, nobody behind). A refused
            // trigger (conditions not met → no session) reads not-resident at
            // once and we return in ~1–2s without holding the FGS.
            return if (awaitSessionClose()) Result.success() else Result.retry()
        } catch (t: Throwable) {
            Log.w(TAG, "sync work failed", t)
            return Result.retry()
        } finally {
            BackendOwnership.release(owner)
            if (role == PowerLogic.ROLE_SCHEDULED) {
                try {
                    SyncScheduler.enqueueNextScheduled(applicationContext)
                } catch (t: Throwable) {
                    Log.w(TAG, "rescheduling next scheduled run failed", t)
                }
            }
        }
    }

    private suspend fun awaitBackendReady(): Boolean {
        val deadlineMs = System.currentTimeMillis() + 60_000
        while (System.currentTimeMillis() < deadlineMs) {
            if (Mobile.isRunning()) return true
            if (isStopped) return false
            delay(1_000)
        }
        return Mobile.isRunning()
    }

    // Returns true if the gate closed the session normally; false if WorkManager
    // stopped us first (constraint loss / Android 14 dataSync ~6h FGS cap) — in
    // which case the caller retries so the worker re-attaches its foreground.
    // ST keeps running under the gate independent of the worker, so the transfer
    // itself isn't interrupted across a retry.
    private suspend fun awaitSessionClose(): Boolean {
        val hardCapMs = System.currentTimeMillis() + 6L * 60 * 60 * 1000
        while (System.currentTimeMillis() < hardCapMs) {
            if (isStopped) return false
            val resident = try {
                Mobile.shouldStayResident()
            } catch (t: Throwable) {
                // If we can't read the gate, don't hold the FGS forever.
                Log.w(TAG, "shouldStayResident read failed", t)
                false
            }
            if (!resident) return true
            delay(POLL_INTERVAL_MS)
        }
        return true
    }

    private fun buildForegroundInfo(): ForegroundInfo {
        val notif = SyncNotification.build(applicationContext)
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ForegroundInfo(
                SyncNotification.WORKER_ID,
                notif,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
            )
        } else {
            ForegroundInfo(SyncNotification.WORKER_ID, notif)
        }
    }

    companion object {
        private const val TAG = "WeSync.SyncWorker"
        private const val POLL_INTERVAL_MS = 20_000L

        // How long to let WiFi associate after a wake before the gate gives up,
        // but only when the WiFi radio is on (we're likely home). Rides the
        // existing wake; bailed instantly when WiFi is off (away).
        private const val NETWORK_SETTLE_MS = 12_000L

        // Input-data key; role values live in PowerLogic (ROLE_TRIGGER/POLL/SCHEDULED).
        const val KEY_ROLE = "role"
    }
}
