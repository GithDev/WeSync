package com.wesync.app

import android.util.Log
import mobile.Mobile
import java.net.HttpURLConnection
import java.net.URL

// LocalBackend wraps the two loopback HTTP calls the activity makes to the
// in-process Go backend (everything else goes through the WebView or the
// gomobile `Mobile` surface):
//
//   pollUntilReady — wait for /api/status to answer, then hand back the port
//                    so the activity can swap in the live WebView.
//   notifyActive   — PUT /api/active, the explicit foreground/background
//                    signal that toggles UDP announce + the peer wire.
//
// Both run their network I/O on a background thread (the platform forbids it
// on the main thread). Callbacks are invoked on that thread — the caller
// marshals back to the UI thread as needed.
object LocalBackend {

    private const val TAG = "WeSync.LocalBackend"

    // Polls /api/status until it answers (or 90s elapses). On success calls
    // onReady(port); on timeout calls log() with a diagnostic. Runs on its
    // own thread and returns immediately.
    fun pollUntilReady(log: (String) -> Unit, onReady: (Long) -> Unit) {
        Thread {
            val port = Mobile.apiPort()
            val url = "http://127.0.0.1:$port/api/status"
            val deadlineMs = System.currentTimeMillis() + 90_000
            var lastErr: String? = null
            while (System.currentTimeMillis() < deadlineMs) {
                try {
                    val conn = URL(url).openConnection() as HttpURLConnection
                    conn.connectTimeout = 500
                    conn.readTimeout = 500
                    val code = conn.responseCode
                    conn.disconnect()
                    if (code in 200..399) {
                        onReady(port)
                        return@Thread
                    }
                    lastErr = "HTTP $code"
                } catch (e: Exception) {
                    lastErr = e.javaClass.simpleName + ": " + (e.message ?: "<no message>")
                }
                Thread.sleep(500)
            }
            val goErr = Mobile.lastError()
            log(
                "Backend did not come up on :$port within 90s\n" +
                    "Last probe error: $lastErr\n" +
                    "Mobile.lastError(): " + (if (goErr.isEmpty()) "(empty)" else goErr),
            )
        }.start()
    }

    // Fire-and-forget PUT /api/active. Failures are logged, never thrown —
    // a lifecycle callback must not block or crash on a flaky local socket.
    fun notifyActive(active: Boolean) {
        val port = Mobile.apiPort()
        Thread {
            try {
                val conn = URL("http://127.0.0.1:$port/api/active").openConnection() as HttpURLConnection
                conn.requestMethod = "PUT"
                conn.connectTimeout = 3000
                conn.readTimeout = 3000
                conn.doOutput = true
                conn.setRequestProperty("Content-Type", "application/json")
                conn.outputStream.use { it.write("{\"active\":$active}".toByteArray()) }
                conn.responseCode // force the request to be sent
                conn.disconnect()
            } catch (t: Throwable) {
                Log.w(TAG, "notifyActive($active) failed", t)
            }
        }.start()
    }
}
