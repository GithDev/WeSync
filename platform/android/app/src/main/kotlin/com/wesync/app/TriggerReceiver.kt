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
        when (intent?.action) {
            ACTION_FIRE -> {
                Log.i(TAG, "safety-net alarm fired")
                try {
                    Mobile.onTriggerAlarm()
                } catch (t: Throwable) {
                    Log.w(TAG, "Mobile.onTriggerAlarm failed", t)
                }
                rearmService(context, ACTION_REARM)
            }
            ACTION_FIRE_POLL -> {
                Log.i(TAG, "poll alarm fired")
                try {
                    Mobile.onTriggerPollAlarm()
                } catch (t: Throwable) {
                    Log.w(TAG, "Mobile.onTriggerPollAlarm failed", t)
                }
                rearmService(context, ACTION_REARM_POLL)
            }
        }
    }

    private fun rearmService(context: Context, rearmAction: String) {
        // Use startForegroundService: we're woken in the background, where a
        // plain startService is rejected on API 26+ outside the alarm's allowlist.
        try {
            val svc = Intent(context, WeSyncService::class.java).setAction(rearmAction)
            context.startForegroundService(svc)
        } catch (t: Throwable) {
            Log.w(TAG, "re-arm dispatch failed for $rearmAction", t)
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
