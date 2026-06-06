package com.wesync.app

import android.Manifest
import android.content.pm.PackageManager
import android.os.Environment
import android.provider.DocumentsContract
import android.util.Log
import android.webkit.JavascriptInterface
import android.webkit.WebView

// Bridges the WebView's React frontend to native Android features that have
// no Go-side equivalent. Right now that's just the Storage Access Framework
// folder picker — Android 11+ scoped storage forbids the in-process file
// dialog approach we use on desktop.
//
// Methods marked @JavascriptInterface are callable from JS via the global
// `WeSync` object (see MainActivity.addJavascriptInterface). They run on a
// background thread, so anything UI-related must be posted to the activity.
//
// The pickFolder bridge is intentionally async-via-callback-id: SAF returns
// via onActivityResult, which can take seconds while the user navigates the
// system picker. The JS side holds a Promise keyed by callbackId; Kotlin
// resolves it later by evaluating window.__weSyncPickResult(id, path).
class JsBridge(
    private val activity: MainActivity,
    private val webView: () -> WebView?,
) {
    @JavascriptInterface
    fun pickFolder(callbackId: String) {
        Log.i(TAG, "pickFolder($callbackId)")
        activity.launchFolderPicker(callbackId)
    }

    // Called by the React UI right after a successful PUT /api/power so
    // the Android service re-fetches the settings and re-arms AlarmManager
    // immediately, rather than waiting for the next natural event.
    @JavascriptInterface
    fun notifyPowerSettingsChanged() {
        Log.i(TAG, "notifyPowerSettingsChanged")
        activity.onPowerSettingsChanged()
    }

    // Advances the location grant one step toward what Trusted WiFi needs:
    // foreground location first, then background ("Allow all the time") —
    // Android reveals the WiFi SSID only with location permission, and only
    // to a backgrounded app with the background variant. Safe to call
    // repeatedly; LocationStep figures out which step is next. On API 30+ the
    // background step routes to the app's settings page.
    @JavascriptInterface
    fun requestLocationPermission() {
        Log.i(TAG, "requestLocationPermission")
        activity.runOnUiThread { LocationStep.request(activity) }
    }

    // True only when location is granted "all the time" (foreground +
    // background on API 29+) — i.e. the gate can read the SSID even while the
    // app is closed, which is when it actually needs to.
    @JavascriptInterface
    fun isLocationGranted(): Boolean = LocationStep.granted(activity)

    // True when at least foreground ("while using the app") location is
    // granted. Lets the UI tell "needs granting" apart from "granted but not
    // set to Always" and warn precisely about the latter.
    @JavascriptInterface
    fun isForegroundLocationGranted(): Boolean =
        activity.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) ==
            PackageManager.PERMISSION_GRANTED

    fun resolvePickFolder(callbackId: String, path: String?) {
        val wv = webView() ?: return
        val payload = if (path == null) "null" else "\"${escapeJs(path)}\""
        val js = "window.__weSyncPickResult && window.__weSyncPickResult(\"$callbackId\", $payload);"
        wv.post { wv.evaluateJavascript(js, null) }
    }

    private fun escapeJs(s: String): String =
        s.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n")

    companion object {
        private const val TAG = "WeSync.JsBridge"

        // Converts an Android Storage Access Framework tree URI into a real
        // filesystem path. Only primary external storage is supported in
        // v1 — that covers ~95% of phones. SD cards and cloud providers
        // surface as different authorities/types and would need extra work
        // (and Fork would need separate permission for them too).
        //
        // Returns null if the URI points anywhere we can't resolve.
        fun treeUriToPath(treeDocumentId: String): String? {
            // Format is "<storage-type>:<relative-path>", e.g.
            //   "primary:Sync/Photos"          → /storage/emulated/0/Sync/Photos
            //   "primary:"                     → /storage/emulated/0 (root)
            //   "1A2B-3C4D:DCIM/Camera"        → SD card; not supported
            val colon = treeDocumentId.indexOf(':')
            if (colon < 0) return null
            val storageType = treeDocumentId.substring(0, colon)
            val relPath = treeDocumentId.substring(colon + 1)
            if (storageType != "primary") return null
            val root = Environment.getExternalStorageDirectory().absolutePath
            return if (relPath.isEmpty()) root else "$root/$relPath"
        }

        fun treeUriToPath(uri: android.net.Uri): String? {
            val docId = try {
                DocumentsContract.getTreeDocumentId(uri)
            } catch (t: Throwable) {
                Log.w(TAG, "getTreeDocumentId failed for $uri", t)
                return null
            }
            return treeUriToPath(docId)
        }
    }
}
