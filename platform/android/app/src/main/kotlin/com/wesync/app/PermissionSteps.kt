package com.wesync.app

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.PowerManager
import android.provider.Settings

// Every runtime permission WeSync needs, modelled as a small interface so
// MainActivity can render a uniform checklist instead of N branches with
// different SDK gates and grant flows. Anything we'd want to ask the user
// about goes here — keep this list authoritative.
//
// "Granted" semantics: returns true if WeSync can do the thing right now,
// regardless of whether that's because the user opted in, the OS auto-
// granted it at install, or the SDK level doesn't require the permission
// at all. Render-wise, those all become a green check.
interface PermissionStep {
    val title: String
    val why: String
    val required: Boolean
    fun granted(ctx: Context): Boolean
    fun request(activity: Activity)
}

// MANAGE_EXTERNAL_STORAGE — the bundled Syncthing reads/writes user-picked
// folders via POSIX I/O. Without this, every path under /storage/emulated/0
// returns EACCES at scan time and sync silently fails. Granted via system
// settings, not an in-app dialog.
object AllFilesAccessStep : PermissionStep {
    override val title = "All files access"
    override val why = "Required so Syncthing can read and write the folders you choose to share."
    override val required = true

    override fun granted(ctx: Context): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            // Pre-30 falls under READ/WRITE_EXTERNAL_STORAGE which the OS
            // grants at install time on those API levels.
            true
        }
    }

    override fun request(activity: Activity) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return
        try {
            val intent = Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION).apply {
                data = Uri.parse("package:${activity.packageName}")
            }
            activity.startActivity(intent)
        } catch (_: Throwable) {
            try {
                activity.startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
            } catch (_: Throwable) {
                // No reachable settings screen — extremely rare (only on
                // stripped-down AOSP builds). Caller logs the message.
            }
        }
    }
}

// POST_NOTIFICATIONS — runtime permission on API 33+. Without it, our
// foreground-service notification can be hidden by the OS, which makes the
// "is WeSync running?" question impossible for the user to answer.
// On older Android the OS auto-grants.
object NotificationsStep : PermissionStep {
    override val title = "Show notifications"
    override val why = "WeSync keeps a small notification visible so Android lets it sync in the background."
    override val required = false // foreground service runs even without it; UX is what suffers

    override fun granted(ctx: Context): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return true
        return ctx.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
    }

    override fun request(activity: Activity) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
        activity.requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIFICATIONS)
    }

    const val REQ_NOTIFICATIONS = 5001
}

// LocationStep — only needed when the user picks "Trusted WiFi" in the
// power gate. Android only reveals the WiFi SSID to an app with location
// permission, and only to a BACKGROUNDED app if it also holds
// ACCESS_BACKGROUND_LOCATION ("Allow all the time") — which is exactly when
// the trusted-network gate needs it (the app is closed when a trigger
// fires). So this is a two-step grant: foreground location first, then
// background. Marked optional so boot never blocks on it; if the user never
// enables Trusted WiFi, neither permission is ever requested.
object LocationStep : PermissionStep {
    override val title = "Location (all the time)"
    override val why = "Only for the \"my own WiFi\" sync gate. Android reveals the WiFi name to apps with location permission; \"all the time\" is required so it works while WeSync runs in the background."
    override val required = false

    override fun granted(ctx: Context): Boolean {
        val fine = ctx.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) ==
            PackageManager.PERMISSION_GRANTED
        if (!fine) return false
        // Background location is a distinct permission from API 29 (Q). With
        // only foreground location the SSID reads null whenever the app isn't
        // visible — useless for the gate. Below Q, foreground location covers
        // the background case too.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return true
        return ctx.checkSelfPermission(Manifest.permission.ACCESS_BACKGROUND_LOCATION) ==
            PackageManager.PERMISSION_GRANTED
    }

    override fun request(activity: Activity) {
        val fineGranted = activity.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) ==
            PackageManager.PERMISSION_GRANTED
        if (!fineGranted) {
            // Step 1. Android 11+ rejects asking for background in the same
            // call, so foreground must land first; the result handler chains
            // to step 2.
            activity.requestPermissions(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION), REQ_LOCATION)
            return
        }
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return
        val bgGranted = activity.checkSelfPermission(Manifest.permission.ACCESS_BACKGROUND_LOCATION) ==
            PackageManager.PERMISSION_GRANTED
        if (bgGranted) return
        if (Build.VERSION.SDK_INT == Build.VERSION_CODES.Q) {
            // Android 10 still shows a direct runtime dialog for background.
            activity.requestPermissions(
                arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION),
                REQ_BACKGROUND_LOCATION,
            )
        } else {
            // Android 11+ won't surface "Allow all the time" in a dialog —
            // the user must pick it on the app's settings page. Send them
            // straight there.
            try {
                activity.startActivity(
                    Intent(
                        Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                        Uri.parse("package:${activity.packageName}"),
                    ),
                )
            } catch (_: Throwable) {
            }
        }
    }

    const val REQ_LOCATION = 5002
    const val REQ_BACKGROUND_LOCATION = 5003
}

// BatteryOptimizationStep — asks Android to exempt WeSync from Doze/battery
// optimization. This is the single biggest factor in whether the foreground
// service survives in the background: without it, aggressive OEMs reclaim the
// process within minutes and the bundled Syncthing dies with it. Optional so
// boot never blocks on it, but we prompt once at startup. Below API 23 there
// is no battery-optimization concept, so it reports granted.
object BatteryOptimizationStep : PermissionStep {
    override val title = "Unrestricted battery"
    override val why = "Lets WeSync keep syncing in the background. Without it, Android may kill it within minutes."
    override val required = false

    override fun granted(ctx: Context): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return true
        val pm = ctx.getSystemService(Context.POWER_SERVICE) as PowerManager
        return pm.isIgnoringBatteryOptimizations(ctx.packageName)
    }

    @SuppressLint("BatteryLife") // intentional: a background sync engine is a valid use
    override fun request(activity: Activity) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) return
        try {
            activity.startActivity(
                Intent(
                    Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                    Uri.parse("package:${activity.packageName}"),
                ),
            )
        } catch (_: Throwable) {
            // Fallback: the full battery-optimization list, where the user can
            // find WeSync manually.
            try {
                activity.startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
            } catch (_: Throwable) {
            }
        }
    }
}

val ALL_PERMISSION_STEPS: List<PermissionStep> = listOf(
    AllFilesAccessStep,
    NotificationsStep,
    LocationStep,
    BatteryOptimizationStep,
)
