package com.wesync.app

import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log

// WeSyncService is the foreground host for the Go backend WHILE THE UI IS OPEN.
// The WebView talks to the in-process Go HTTP API, so the backend must stay up
// as long as the activity is around. Background syncing is NOT this service's
// job anymore — that's WorkManager (SyncScheduler + SyncWorker). The service no
// longer schedules anything, owns no alarms, and does not self-stop on a timer.
//
// Lifecycle:
//   MainActivity.onResume  → HOLD_UI  → acquire backend, (re)arm the schedule
//   MainActivity.onPause   → RELEASE_UI → after a short grace, release + stopSelf
//   (transient background — folder picker, permission dialog — is covered by the
//    grace, so the backend isn't torn down only to be needed again seconds later)
//
// Backend lifetime + the radio/CPU locks live in BackendOwnership, the single
// process-global owner; this service just holds the "ui" token while visible.
class WeSyncService : Service() {

    private var power: PowerController? = null
    private val main = Handler(Looper.getMainLooper())
    private var powerStarted = false

    // Releases the UI's claim on the backend after the grace. BackendOwnership
    // decides whether that actually stops the Go backend (it won't if a
    // background sync is still finishing).
    private val releaseUiRunnable = Runnable {
        Log.i(TAG, "UI grace elapsed — releasing backend ownership")
        BackendOwnership.release(BackendOwnership.OWNER_UI)
        stopSelf()
    }

    override fun onCreate() {
        super.onCreate()
        SyncNotification.ensureChannel(this)
        power = PowerController(applicationContext)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val notif = SyncNotification.build(this)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(SyncNotification.SERVICE_ID, notif, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(SyncNotification.SERVICE_ID, notif)
        }

        when (intent?.action) {
            ACTION_RELEASE_UI -> {
                main.removeCallbacks(releaseUiRunnable)
                main.postDelayed(releaseUiRunnable, UI_GRACE_MS)
                Log.i(TAG, "UI left — releasing in ${UI_GRACE_MS / 1000}s")
            }
            ACTION_REFRESH_NETWORK -> {
                holdUi()
                power?.refreshNetwork()
            }
            else -> holdUi() // HOLD_UI, START_BACKEND, initial (null)
        }
        return START_STICKY
    }

    // The UI is present (or returned): keep the backend up, start the live
    // power listeners once, and make sure the WorkManager schedule is armed.
    private fun holdUi() {
        main.removeCallbacks(releaseUiRunnable)
        BackendOwnership.acquire(applicationContext, BackendOwnership.OWNER_UI)
        if (!powerStarted) {
            powerStarted = true
            power?.start()
            // Arm (or refresh) the background schedule now that the backend is
            // coming up. Idempotent; ReapplyScheduleWorker waits for the gate.
            SyncScheduler.reapply(applicationContext)
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    // Swiped from recents: same as leaving the UI — schedule the release.
    override fun onTaskRemoved(rootIntent: Intent?) {
        Log.i(TAG, "task removed — scheduling UI release")
        main.removeCallbacks(releaseUiRunnable)
        main.postDelayed(releaseUiRunnable, UI_GRACE_MS)
        super.onTaskRemoved(rootIntent)
    }

    override fun onDestroy() {
        Log.i(TAG, "service stopping")
        main.removeCallbacks(releaseUiRunnable)
        power?.stop()
        power = null
        // Defensive: drop the UI claim if we're being destroyed for any other
        // reason. BackendOwnership won't stop the Go backend if a background
        // sync is still in flight.
        BackendOwnership.release(BackendOwnership.OWNER_UI)
        super.onDestroy()
    }

    companion object {
        private const val TAG = "WeSync.Service"

        // How long the backend stays warm after the user leaves the UI, so a
        // quick return (or a transient background like the folder picker) doesn't
        // pay a cold start. Background sync no longer depends on this.
        const val UI_GRACE_MS = 5L * 60 * 1000

        const val ACTION_HOLD_UI = "com.wesync.app.HOLD_UI"
        const val ACTION_RELEASE_UI = "com.wesync.app.RELEASE_UI"
        const val ACTION_REFRESH_NETWORK = "com.wesync.app.REFRESH_NETWORK"
        // Sent by MainActivity after permissions are granted so the service runs
        // its hold path (acquire backend + arm schedule) on a fresh install.
        const val ACTION_START_BACKEND = "com.wesync.app.START_BACKEND"
    }
}
