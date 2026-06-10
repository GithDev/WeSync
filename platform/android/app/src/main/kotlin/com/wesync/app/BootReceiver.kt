package com.wesync.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log

// Restarts WeSync after a device reboot so background sync resumes without the
// user reopening the app. Autostart is always on — for a sync app that's the
// expected behaviour. (Android won't deliver BOOT_COMPLETED until the app has
// been launched manually at least once since install, and only if it isn't
// force-stopped — both are platform rules we can't change.)
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        val action = intent?.action ?: return
        if (action != Intent.ACTION_BOOT_COMPLETED &&
            action != "android.intent.action.QUICKBOOT_POWERON"
        ) {
            return
        }
        try {
            context.startForegroundService(
                Intent(context, WeSyncService::class.java).setAction(WeSyncService.ACTION_BOOT),
            )
            Log.i(TAG, "boot autostart: starting service ($action)")
        } catch (t: Throwable) {
            // e.g. ForegroundServiceStartNotAllowedException on some OEM/SDK
            // combinations — log and move on; the alarm armed below recovers it.
            Log.w(TAG, "boot autostart failed", t)
        }
        // The start above is NOT enough on Android 14+: a dataSync foreground
        // service can't be launched from a BOOT_COMPLETED receiver, so it throws
        // and is swallowed — leaving AlarmManager (wiped by the reboot) unarmed
        // and the device asleep until the user reopens the app. Arm a poll alarm
        // as well: when it fires, TriggerReceiver starts the service under the
        // alarm FGS-start exemption (which, unlike the BOOT_COMPLETED exemption,
        // has no per-type exclusion) and the service re-arms the full schedule.
        // Harmless when the direct start did succeed — it shares the live poll
        // alarm's PendingIntent, so the service's reapply() cancels+rearms it.
        try {
            PowerController.scheduleBootKick(context)
        } catch (t: Throwable) {
            Log.w(TAG, "boot kick alarm failed", t)
        }
    }

    companion object {
        private const val TAG = "WeSync.Boot"
    }
}
