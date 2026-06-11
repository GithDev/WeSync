package com.wesync.app

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.util.Calendar
import java.util.TimeZone

// JVM unit tests for the power logic that used to be untestable inside
// framework-coupled classes — the schedule math, wake-plan parsing, SSID
// cleaning and watch-noise filtering. No device, no Robolectric.
class PowerLogicTest {

    // Builds a fixed "now" so the day-rollover branches are deterministic.
    // Fixed to UTC so the test is stable regardless of the build machine's
    // timezone.
    private fun at(year: Int, month: Int, day: Int, hour: Int, minute: Int): Calendar {
        return Calendar.getInstance(TimeZone.getTimeZone("UTC")).apply {
            clear()
            set(year, month - 1, day, hour, minute, 0)
            set(Calendar.MILLISECOND, 0)
        }
    }

    private fun fieldsOf(ms: Long): Triple<Int, Int, Int> {
        val c = Calendar.getInstance(TimeZone.getTimeZone("UTC")).apply { timeInMillis = ms }
        return Triple(c.get(Calendar.DAY_OF_MONTH), c.get(Calendar.HOUR_OF_DAY), c.get(Calendar.MINUTE))
    }

    // ── nextScheduledMillis ────────────────────────────────────────────────

    @Test fun emptyListReturnsNull() {
        assertNull(PowerLogic.nextScheduledMillis(emptyList(), at(2026, 6, 3, 10, 0)))
    }

    @Test fun laterTodayIsPickedSameDay() {
        val now = at(2026, 6, 3, 10, 0)
        val next = PowerLogic.nextScheduledMillis(listOf("19:00"), now)!!
        assertEquals(Triple(3, 19, 0), fieldsOf(next))
    }

    @Test fun earlierTodayRollsToTomorrow() {
        val now = at(2026, 6, 3, 10, 0)
        val next = PowerLogic.nextScheduledMillis(listOf("07:00"), now)!!
        assertEquals(Triple(4, 7, 0), fieldsOf(next))
    }

    @Test fun exactlyNowRollsToTomorrow() {
        // "not after now" must roll forward — otherwise an alarm armed at
        // its own fire-instant would re-fire immediately in a loop.
        val now = at(2026, 6, 3, 10, 0)
        val next = PowerLogic.nextScheduledMillis(listOf("10:00"), now)!!
        assertEquals(Triple(4, 10, 0), fieldsOf(next))
    }

    @Test fun picksSoonestOfSeveral() {
        val now = at(2026, 6, 3, 10, 0)
        // 07:00 already passed (→ tomorrow 07:00), 12:00 and 19:00 are today.
        // Soonest is today 12:00.
        val next = PowerLogic.nextScheduledMillis(listOf("19:00", "07:00", "12:00"), now)!!
        assertEquals(Triple(3, 12, 0), fieldsOf(next))
    }

    @Test fun malformedEntriesAreSkippedNotClamped() {
        val now = at(2026, 6, 3, 10, 0)
        // Garbage and out-of-range entries must be ignored; only 18:30 counts.
        val times = listOf("nonsense", "25:00", "12:99", "8", "18:30", "")
        val next = PowerLogic.nextScheduledMillis(times, now)!!
        assertEquals(Triple(3, 18, 30), fieldsOf(next))
    }

    @Test fun allMalformedReturnsNull() {
        val now = at(2026, 6, 3, 10, 0)
        assertNull(PowerLogic.nextScheduledMillis(listOf("99:99", "x:y", ":"), now))
    }

    @Test fun midnightIsValid() {
        val now = at(2026, 6, 3, 10, 0)
        val next = PowerLogic.nextScheduledMillis(listOf("00:00"), now)!!
        assertEquals(Triple(4, 0, 0), fieldsOf(next)) // already past today → tomorrow
    }

    // ── cleanSsid ──────────────────────────────────────────────────────────

    @Test fun cleanSsidStripsQuotes() {
        assertEquals("HomeNet", PowerLogic.cleanSsid("\"HomeNet\""))
    }

    @Test fun cleanSsidPassesThroughUnquoted() {
        assertEquals("HomeNet", PowerLogic.cleanSsid("HomeNet"))
    }

    @Test fun cleanSsidNullForNullEmptyOrUnknown() {
        assertNull(PowerLogic.cleanSsid(null))
        assertNull(PowerLogic.cleanSsid(""))
        assertNull(PowerLogic.cleanSsid("\"\""))
        assertNull(PowerLogic.cleanSsid("<unknown ssid>"))
    }

    // ── WakePlan.fromJson ────────────────────────────────────────────────────

    @Test fun wakePlanParsesFullPayload() {
        val json = JSONObject(
            """
            {"mode":"scheduled","periodicMinutes":30,
             "scheduledTimes":["07:00","19:00"]}
            """.trimIndent(),
        )
        val p = WakePlan.fromJson(json)
        assertEquals("scheduled", p.mode)
        assertEquals(30, p.periodicMinutes)
        assertEquals(listOf("07:00", "19:00"), p.scheduledTimes)
    }

    @Test fun wakePlanAppliesDefaultsForMissingFields() {
        val p = WakePlan.fromJson(JSONObject("{}"))
        assertEquals("", p.mode)
        assertEquals(120, p.periodicMinutes)
        assertEquals(emptyList<String>(), p.scheduledTimes)
    }

    @Test fun wakePlanDropsEmptyScheduledEntries() {
        val json = JSONObject("""{"mode":"scheduled","scheduledTimes":["07:00","","19:00"]}""")
        val p = WakePlan.fromJson(json)
        assertEquals(listOf("07:00", "19:00"), p.scheduledTimes)
    }

    // ── clampWakeIntervalMinutes (the 15-min WorkManager floor) ──────────────

    @Test fun belowFloorIsClampedUpTo15() {
        // The gate's onChangePollMinutes can be smaller than WorkManager allows;
        // every requested interval below 15 must be raised to 15.
        assertEquals(15L, PowerLogic.clampWakeIntervalMinutes(1))
        assertEquals(15L, PowerLogic.clampWakeIntervalMinutes(5))
        assertEquals(15L, PowerLogic.clampWakeIntervalMinutes(14))
        assertEquals(15L, PowerLogic.clampWakeIntervalMinutes(0))
    }

    @Test fun atOrAboveFloorIsUnchanged() {
        assertEquals(15L, PowerLogic.clampWakeIntervalMinutes(15))
        assertEquals(30L, PowerLogic.clampWakeIntervalMinutes(30))
        assertEquals(240L, PowerLogic.clampWakeIntervalMinutes(240))
    }
}
