package com.wesync.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import mobile.Mobile

// Fires when AlarmManager wakes us for a periodic interval, a scheduled time,
// or an on_change_poll snapshot check. Just hands the wake to the gate via
// Mobile.onTriggerAlarm() — the gate decides what it means: periodic/scheduled
// always open a sync window; on_change_poll checks directory mtimes and opens
// one regardless (the snapshot result is informational/logged only).
// Network/battery gates may still keep ST off after that.
//
// For scheduled mode we also need to re-arm the next occurrence after we
// fire (setAndAllowWhileIdle is one-shot). PowerController.reapply()
// handles that — we kick it via a service-bound Intent.
class TriggerReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        Log.i(TAG, "trigger fired")
        try {
            Mobile.onTriggerAlarm()
        } catch (t: Throwable) {
            Log.w(TAG, "Mobile.onTriggerAlarm failed", t)
        }
        // Re-arm the next alarm (scheduled needs it after every one-shot
        // fire; periodic re-arms itself the same way). Use
        // startForegroundService: we're woken in the background, where a
        // plain startService would be rejected on API 26+ outside the
        // alarm's short temporary allowlist.
        try {
            val svc = Intent(context, WeSyncService::class.java).setAction(ACTION_REARM)
            context.startForegroundService(svc)
        } catch (t: Throwable) {
            Log.w(TAG, "re-arm dispatch failed", t)
        }
    }

    companion object {
        const val ACTION_FIRE = "com.wesync.app.TRIGGER_FIRE"
        const val ACTION_REARM = "com.wesync.app.TRIGGER_REARM"
        private const val TAG = "WeSync.Trigger"
    }
}
