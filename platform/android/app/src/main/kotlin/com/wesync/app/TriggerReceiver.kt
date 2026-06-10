package com.wesync.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import mobile.Mobile

// Fires when AlarmManager wakes us. Two alarm types exist for on_change_poll:
//
//   ACTION_FIRE      — safety-net / periodic / scheduled alarm.
//                      Calls Mobile.onTriggerAlarm() which always opens a session.
//   ACTION_FIRE_POLL — fast change-detection alarm (on_change_poll only).
//                      Calls Mobile.onTriggerPollAlarm() which opens a session
//                      only when directory mtimes show structural changes.
//
// Each action re-arms only its OWN alarm (via ACTION_REARM / ACTION_REARM_POLL)
// so the two alarms are independent and don't reset each other's countdown.
class TriggerReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        val action = intent?.action ?: return
        if (action != ACTION_FIRE && action != ACTION_FIRE_POLL) return
        // Hand off to WeSyncService so Mobile.onTrigger*Alarm() is called after
        // the Go backend is running. Calling Mobile.* directly here fails
        // silently when the service (and backend) have self-stopped between alarms.
        Log.i(TAG, "alarm received: $action — dispatching to service")
        try {
            context.startForegroundService(
                Intent(context, WeSyncService::class.java).setAction(action)
            )
        } catch (t: Throwable) {
            Log.w(TAG, "dispatch failed for $action", t)
        }
    }

    companion object {
        const val ACTION_FIRE = "com.wesync.app.TRIGGER_FIRE"
        const val ACTION_FIRE_POLL = "com.wesync.app.TRIGGER_FIRE_POLL"
        const val ACTION_REARM = "com.wesync.app.TRIGGER_REARM"
        const val ACTION_REARM_POLL = "com.wesync.app.TRIGGER_REARM_POLL"
        private const val TAG = "WeSync.Trigger"
    }
}
