package com.wesync.app

import android.app.Activity
import android.graphics.Color
import android.graphics.Typeface
import android.os.Handler
import android.os.Looper
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import mobile.Mobile

// SetupScreen owns the pre-WebView "Setting things up" UI: the permission
// checklist (driven by PermissionSteps.kt), the boot log, and the persistent
// "Recent activity" log polled off the gate. It builds its views entirely in
// code (no XML) and exposes a few narrow hooks to MainActivity:
//
//   view                  — the root View to drop into the activity
//   renderPermissions()   — (re)draw the checklist, returns "all required granted?"
//   appendLog(msg)        — append a line to the boot log
//   start/stopActivityPolling() — drive the Recent-activity panel
//
// Pulling this out of MainActivity keeps the activity focused on lifecycle +
// delegation rather than ~130 lines of manual view construction.
class SetupScreen(private val activity: Activity) {

    private val main = Handler(Looper.getMainLooper())

    private val permissionsContainer: LinearLayout
    private val logText: TextView
    private val logScroll: ScrollView
    private val activityText: TextView
    private val activityScroll: ScrollView
    private var activityPoller: Runnable? = null

    // The root view MainActivity adds to its content container.
    val view: View

    init {
        val container = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(24), dp(96), dp(24), dp(16))
        }

        container.addView(
            TextView(activity).apply {
                text = "WeSync"
                setTypeface(typeface, Typeface.BOLD)
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 28f)
                setTextColor(Color.parseColor("#0F172A"))
            },
        )
        container.addView(
            TextView(activity).apply {
                text = "Setting things up"
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 14f)
                setTextColor(Color.parseColor("#64748B"))
                setPadding(0, dp(4), 0, dp(20))
            },
        )

        permissionsContainer = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
        }
        container.addView(permissionsContainer)

        // Log card sits below the checklist; gets the remaining vertical
        // space so long Go output is readable without scrolling the whole
        // setup screen.
        val logCard = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(Color.parseColor("#F1F5F9"))
            setPadding(dp(12), dp(10), dp(12), dp(10))
            val lp = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                0,
                1f, // take all remaining vertical space
            )
            lp.topMargin = dp(20)
            layoutParams = lp
        }
        logCard.addView(
            TextView(activity).apply {
                text = "Boot log"
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 11f)
                setTextColor(Color.parseColor("#64748B"))
                setTypeface(typeface, Typeface.BOLD)
                setPadding(0, 0, 0, dp(4))
            },
        )
        logText = TextView(activity).apply {
            text = ""
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 11f)
            setTextColor(Color.parseColor("#334155"))
            typeface = Typeface.MONOSPACE
        }
        logScroll = ScrollView(activity).apply {
            addView(logText)
            layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.MATCH_PARENT,
            )
        }
        logCard.addView(logScroll)

        container.addView(
            logCard,
            LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                0,
                1f,
            ),
        )

        // Second card — the persistent activity log from power_events.
        // Same shape as the boot log so the two visually pair. Polled
        // off the gate's in-process store, so it lights up as soon as
        // the backend is far enough into Mobile.start to have a DB.
        val activityCard = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(Color.parseColor("#F1F5F9"))
            setPadding(dp(12), dp(10), dp(12), dp(10))
            val lp = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                0,
                1f,
            )
            lp.topMargin = dp(12)
            layoutParams = lp
        }
        activityCard.addView(
            TextView(activity).apply {
                text = "Recent activity"
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 11f)
                setTextColor(Color.parseColor("#64748B"))
                setTypeface(typeface, Typeface.BOLD)
                setPadding(0, 0, 0, dp(4))
            },
        )
        activityText = TextView(activity).apply {
            text = ""
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 11f)
            setTextColor(Color.parseColor("#334155"))
            typeface = Typeface.MONOSPACE
        }
        activityScroll = ScrollView(activity).apply {
            addView(activityText)
            layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.MATCH_PARENT,
            )
        }
        activityCard.addView(activityScroll)
        container.addView(activityCard)

        view = container
    }

    // Renders one row per REQUIRED PermissionStep and returns whether they're
    // all granted (so MainActivity can decide to boot the backend). Optional
    // permissions (notifications, location-for-SSID) are surfaced contextually
    // elsewhere — showing them here alongside the real blockers makes users
    // think they need to grant them.
    //
    // Re-called on every onResume so returning from the system settings
    // reflects the new state without a manual refresh.
    fun renderPermissions(): Boolean {
        permissionsContainer.removeAllViews()
        var allRequiredGranted = true
        for (step in ALL_PERMISSION_STEPS.filter { it.required }) {
            val granted = step.granted(activity)
            if (!granted) allRequiredGranted = false
            permissionsContainer.addView(buildPermissionRow(step, granted))
        }
        return allRequiredGranted
    }

    fun appendLog(msg: String) {
        main.post {
            logText.append(msg + "\n")
            logScroll.post { logScroll.fullScroll(View.FOCUS_DOWN) }
        }
    }

    fun startActivityPolling() {
        stopActivityPolling()
        val poller = object : Runnable {
            override fun run() {
                Thread {
                    val text = try {
                        renderActivity(Mobile.recentPowerEventsJSON(40))
                    } catch (_: Throwable) {
                        null
                    }
                    if (text != null) {
                        main.post {
                            activityText.text = text
                            activityScroll.post { activityScroll.fullScroll(View.FOCUS_DOWN) }
                        }
                    }
                }.start()
                main.postDelayed(this, 3_000)
            }
        }
        activityPoller = poller
        main.post(poller)
    }

    fun stopActivityPolling() {
        activityPoller?.let { main.removeCallbacks(it) }
        activityPoller = null
    }

    // Parses the JSON Mobile gives us and renders one line per event.
    // Keeps formatting consistent with the WebView's activity panel.
    private fun renderActivity(json: String): String {
        return try {
            val arr = org.json.JSONArray(json)
            if (arr.length() == 0) return "(no events yet)"
            val out = StringBuilder()
            // Iterate oldest-first for chronological top-to-bottom flow
            // even though the API hands them newest-first.
            for (i in (arr.length() - 1) downTo 0) {
                val o = arr.optJSONObject(i) ?: continue
                val ts = o.optString("timestamp", "")
                val kind = o.optString("kind", "")
                val msg = o.optString("message", "")
                out.append(ts).append("  ").append(kind).append("  ").append(msg).append('\n')
            }
            out.toString()
        } catch (_: Throwable) {
            "(could not parse events)"
        }
    }

    private fun buildPermissionRow(step: PermissionStep, granted: Boolean): View {
        val row = LinearLayout(activity).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(12), dp(10), dp(12), dp(10))
            // Subtle card background that turns slightly green when granted.
            setBackgroundColor(
                if (granted) Color.parseColor("#F0FDF4") else Color.parseColor("#FFFBEB"),
            )
            val lp = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.WRAP_CONTENT,
            )
            lp.bottomMargin = dp(8)
            layoutParams = lp
        }

        // Status dot.
        row.addView(
            TextView(activity).apply {
                text = if (granted) "✓" else "•"
                setTextColor(
                    if (granted) Color.parseColor("#16A34A") else Color.parseColor("#D97706"),
                )
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 18f)
                setTypeface(typeface, Typeface.BOLD)
                setPadding(0, 0, dp(10), 0)
                width = dp(28)
                gravity = Gravity.CENTER
            },
        )

        // Title + why text.
        val textCol = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
        }
        textCol.addView(
            TextView(activity).apply {
                text = step.title + if (!step.required) " (optional)" else ""
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 14f)
                setTextColor(Color.parseColor("#0F172A"))
                setTypeface(typeface, Typeface.BOLD)
            },
        )
        textCol.addView(
            TextView(activity).apply {
                text = step.why
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 12f)
                setTextColor(Color.parseColor("#475569"))
                setPadding(0, dp(2), 0, 0)
            },
        )
        row.addView(textCol)

        // Grant button (only when not granted).
        if (!granted) {
            row.addView(
                Button(activity).apply {
                    text = "Grant"
                    setOnClickListener { step.request(activity) }
                },
            )
        }

        return row
    }

    private fun dp(value: Int): Int =
        (value * activity.resources.displayMetrics.density).toInt()
}
