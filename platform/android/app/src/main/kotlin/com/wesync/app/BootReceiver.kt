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
            // combinations — log and move on; the user opening the app
            // recovers it.
            Log.w(TAG, "boot autostart failed", t)
        }
    }

    companion object {
        private const val TAG = "WeSync.Boot"
    }
}
