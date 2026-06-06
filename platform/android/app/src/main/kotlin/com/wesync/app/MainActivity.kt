package com.wesync.app

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.View
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.LinearLayout
import mobile.Mobile

// WeSync Android — entrypoint activity.
//
// This class owns only lifecycle + delegation; the heavy lifting lives in
// focused helpers:
//   - SetupScreen   — the "Setting things up" UI (checklist + boot/activity logs)
//   - LocalBackend  — the loopback HTTP to the in-process backend (poll + active)
//   - WeSyncService — owns the Go backend process and the power gate
//   - JsBridge      — the WebView ⇄ native bridge (folder picker, location)
//
// Boot flow:
//   1. Start the foreground service, so the process stays alive and owns
//      the Go backend. The service holds the MulticastLock/WakeLock around
//      sync sessions — radio/CPU ownership lives there, not here.
//   2. Show the SetupScreen: a permission checklist plus live logs.
//   3. As soon as every required permission flips to granted, start the
//      Go backend (Mobile.start, via the service).
//   4. When the backend's HTTP API answers, swap the setup view for the
//      fullscreen WebView pointed at it.
//
// onResume re-checks permissions, so the user returning from the system
// settings screen makes the checklist refresh and the boot unblock.
class MainActivity : Activity() {

    private lateinit var root: LinearLayout
    private lateinit var setup: SetupScreen
    private var liveWebView: WebView? = null
    private val main = Handler(Looper.getMainLooper())
    private val jsBridge = JsBridge(this) { liveWebView }
    private var pendingPickCallbackId: String? = null
    private var backendStarted = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        startForegroundService(Intent(this, WeSyncService::class.java))

        @Suppress("DEPRECATION")
        window.decorView.systemUiVisibility =
            View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN

        root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(Color.WHITE)
        }
        setContentView(root)

        setup = SetupScreen(this)
        root.addView(
            setup.view,
            LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.MATCH_PARENT,
            ),
        )
        setup.startActivityPolling()
        refreshAndMaybeBoot()
    }

    override fun onResume() {
        super.onResume()
        if (liveWebView == null) refreshAndMaybeBoot()
        try {
            Mobile.onAppForeground(true)
        } catch (_: Throwable) {
        }
        // Explicit foreground signal for the WeSync control plane (UDP
        // announce + wire). onAppForeground above only governs the ST
        // process via the gate; the webview's WS stays connected across
        // pause/resume, so it never toggles this on its own — same reason
        // the desktop app drives /api/active explicitly.
        notifyActive(true)
        // UI is back — cancel any pending service shutdown so we don't
        // tear down ST under the user's nose.
        try {
            startService(Intent(this, WeSyncService::class.java).setAction(WeSyncService.ACTION_CANCEL_SHUTDOWN))
        } catch (_: Throwable) {
        }
        // Returning from the system "Allow all the time" location page lands
        // here (Android 11+ grants background location via Settings, not a
        // dialog). Re-read the SSID now so the trusted-WiFi gate sees the new
        // grant without an app restart.
        refreshNetworkState()
    }

    override fun onPause() {
        super.onPause()
        try {
            Mobile.onAppForeground(false)
        } catch (_: Throwable) {
        }
        // Silence the WeSync control plane immediately (discovery + wire),
        // mirroring desktop's hide-to-tray. Without this the still-open
        // webview WS keeps the node "foreground" and UDP announce never stops.
        notifyActive(false)
        // User left — start the grace timer. If they come back within
        // GRACE_MS we just cancel; otherwise the service stops itself
        // and the process dies (zero background overhead).
        try {
            val intent = Intent(this, WeSyncService::class.java)
                .setAction(WeSyncService.ACTION_SCHEDULE_SHUTDOWN)
            startService(intent)
        } catch (_: Throwable) {
        }
    }

    override fun onDestroy() {
        // Do NOT stop the service or the Go backend here — the activity
        // can be destroyed for many reasons (config change, low memory,
        // user task-swiping) and the service owns its own lifecycle via
        // the shutdownRunnable. Killing Mobile.stop() here was making
        // every screen rotation tear ST down behind the user's back.
        super.onDestroy()
    }

    // ── Setup / boot ──────────────────────────────────────────────────────

    // Re-render the checklist; once every required permission is granted,
    // kick the service to (re)boot the backend and start polling for it.
    private fun refreshAndMaybeBoot() {
        val allRequiredGranted = setup.renderPermissions()
        if (allRequiredGranted && !backendStarted) {
            backendStarted = true
            // The foreground service may have been created BEFORE
            // permission was granted (and thus skipped Mobile.start in
            // its onCreate). Send a no-op intent so its onStartCommand
            // re-runs startBackendIfReady now that the permission is
            // actually in place. Mobile.start is idempotent.
            try {
                startService(Intent(this, WeSyncService::class.java))
            } catch (_: Throwable) {
            }
            // Quietly nudge optional permissions while we're booting —
            // notifications in particular, since Android 13+ silently
            // hides our foreground-service notification without it.
            requestOptionalsOnceAfterBoot()
            startBackend()
        }
    }

    private var optionalsRequested = false
    private fun requestOptionalsOnceAfterBoot() {
        if (optionalsRequested) return
        optionalsRequested = true
        // POST_NOTIFICATIONS and battery-optimization-exemption both auto-
        // prompt — the latter is what actually keeps the foreground service
        // (and bundled Syncthing) alive in the background on aggressive OEMs.
        // LocationStep stays gated behind the user picking "Trusted WiFi".
        if (!NotificationsStep.granted(this)) {
            try {
                NotificationsStep.request(this)
            } catch (_: Throwable) {
            }
        }
        if (!BatteryOptimizationStep.granted(this)) {
            try {
                BatteryOptimizationStep.request(this)
            } catch (_: Throwable) {
            }
        }
    }

    // The Go backend is owned by WeSyncService; here we just wait for
    // /api/status to answer and then swap to the WebView. This split lets
    // background triggers (alarms, file-change events) wake the backend
    // without needing the activity.
    private fun startBackend() {
        setup.appendLog("dataDir: ${filesDir.absolutePath}")
        setup.appendLog("Waiting for service-side backend to come up…")
        LocalBackend.pollUntilReady(
            log = { setup.appendLog(it) },
            onReady = { port -> main.post { switchToLiveWebView(port) } },
        )
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == LocationStep.REQ_LOCATION) {
            // Foreground location just resolved. If granted, chain straight to
            // the background ("Allow all the time") step — otherwise the user
            // is left with a half-granted Trusted WiFi gate that silently
            // can't read the SSID while the app is closed.
            if (grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
                LocationStep.request(this)
                // Foreground location is now enough to read the SSID — refresh
                // it immediately so Trusted WiFi works without a restart.
                refreshNetworkState()
            }
            refreshAndMaybeBoot()
            return
        }
        // POST_NOTIFICATIONS and the background-location step flow through the
        // standard dialog — re-render so the checklist updates immediately.
        if (requestCode == NotificationsStep.REQ_NOTIFICATIONS ||
            requestCode == LocationStep.REQ_BACKGROUND_LOCATION) {
            if (requestCode == LocationStep.REQ_BACKGROUND_LOCATION) {
                refreshNetworkState()
            }
            refreshAndMaybeBoot()
        }
    }

    // notifyActive PUTs the foreground/background state to the backend's
    // /api/active. Skipped until the backend is up (liveWebView != null) so we
    // don't hammer a port that isn't answering yet during the setup screen.
    private fun notifyActive(active: Boolean) {
        if (liveWebView == null) return
        LocalBackend.notifyActive(active)
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun switchToLiveWebView(port: Long) {
        setup.stopActivityPolling()
        liveWebView = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            webViewClient = WebViewClient()
            webChromeClient = WebChromeClient()
            addJavascriptInterface(jsBridge, "WeSync")
            loadUrl("http://127.0.0.1:$port/")
        }
        root.removeAllViews()
        root.addView(
            liveWebView,
            LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.MATCH_PARENT,
            ),
        )
    }

    @Suppress("DEPRECATION")
    override fun onBackPressed() {
        val wv = liveWebView
        if (wv != null && wv.canGoBack()) wv.goBack() else super.onBackPressed()
    }

    // ── Bridge-driven actions (called by JsBridge) ────────────────────────

    fun onPowerSettingsChanged() {
        // Bounce through the service intent so PowerController can re-
        // arm alarms and call Mobile.refreshPowerSettings off the main
        // thread.
        try {
            startService(Intent(this, WeSyncService::class.java).setAction(TriggerReceiver.ACTION_REARM))
        } catch (t: Throwable) {
            Log.w(TAG, "onPowerSettingsChanged dispatch failed", t)
        }
    }

    // Tells PowerController to re-read the WiFi SSID by re-registering its
    // network callback. Needed because granting location fires no network
    // event — without this the trusted-WiFi gate keeps a blank SSID until an
    // app restart. Only fires once foreground location is actually granted
    // (PowerController no-ops otherwise, but gating here avoids waking the
    // service on every resume for users who never enable Trusted WiFi).
    private fun refreshNetworkState() {
        if (checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED) {
            return
        }
        try {
            startService(Intent(this, WeSyncService::class.java).setAction(WeSyncService.ACTION_REFRESH_NETWORK))
        } catch (t: Throwable) {
            Log.w(TAG, "refreshNetworkState dispatch failed", t)
        }
    }

    fun launchFolderPicker(callbackId: String) {
        pendingPickCallbackId = callbackId
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        main.post {
            try {
                @Suppress("DEPRECATION")
                startActivityForResult(intent, REQ_PICK_FOLDER)
            } catch (t: Throwable) {
                Log.e(TAG, "ACTION_OPEN_DOCUMENT_TREE not available", t)
                resolvePick(null)
            }
        }
    }

    @Deprecated("Plain Activity has no ActivityResultLauncher; old API is what works here")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode != REQ_PICK_FOLDER) {
            @Suppress("DEPRECATION")
            super.onActivityResult(requestCode, resultCode, data)
            return
        }
        if (resultCode != Activity.RESULT_OK || data == null) {
            resolvePick(null)
            return
        }
        val uri = data.data
        if (uri == null) {
            resolvePick(null)
            return
        }
        val path = JsBridge.treeUriToPath(uri)
        if (path == null) {
            Log.w(TAG, "Picked URI not resolvable to a path: $uri")
        }
        resolvePick(path)
    }

    private fun resolvePick(path: String?) {
        val id = pendingPickCallbackId ?: return
        pendingPickCallbackId = null
        jsBridge.resolvePickFolder(id, path)
    }

    companion object {
        private const val TAG = "WeSync.MainActivity"
        private const val REQ_PICK_FOLDER = 4711
    }
}
