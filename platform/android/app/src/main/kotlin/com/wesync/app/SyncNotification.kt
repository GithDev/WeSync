package com.wesync.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build

// Shared foreground-service notification, used by BOTH the UI-foreground
// service (WeSyncService) and the WorkManager sync worker (SyncWorker). They
// run at different times but present the same ongoing "WeSync is running"
// notification. Distinct notification IDs (SERVICE_ID vs WORKER_ID) so that if
// both are briefly alive at once neither cancels the other's notification.
object SyncNotification {
    const val CHANNEL_ID = "wesync-sync"
    const val SERVICE_ID = 1001
    const val WORKER_ID = 1002

    // Notification channels exist only on API 26+. No-op below that.
    fun ensureChannel(ctx: Context) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val nm = ctx.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (nm.getNotificationChannel(CHANNEL_ID) != null) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            "WeSync running",
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = "Shown only while WeSync is actively syncing or open"
            setShowBadge(false)
        }
        nm.createNotificationChannel(channel)
    }

    fun build(ctx: Context): Notification {
        ensureChannel(ctx)
        val tapIntent = PendingIntent.getActivity(
            ctx,
            0,
            Intent(ctx, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        // The channel-aware Builder constructor is API 26+; below that the
        // deprecated channel-less constructor is the only option.
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(ctx, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(ctx)
        }
        return builder
            .setContentTitle("WeSync")
            .setContentText("Running in the background")
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .setContentIntent(tapIntent)
            .build()
    }
}
