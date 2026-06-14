package com.wesync.app

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

// Integration tests for the WorkManager scheduling glue that PowerLogicTest's
// pure planWorks() can't reach: that SyncScheduler.enqueueFromPlan actually
// arms the right unique work for each wake-plan mode, and — the part that bit
// us on-device — that switching modes cancels the stale work instead of
// leaving two schedulers running.
//
// Runs on the plain JVM via Robolectric + work-testing (no device). It only
// exercises enqueueFromPlan(ctx, plan), which takes a WakePlan directly and
// never calls the Mobile JNI surface; the work stays ENQUEUED (we never drive
// the TestDriver), so SyncWorker.doWork — which does call Mobile — never runs.
@RunWith(RobolectricTestRunner::class)
class SyncSchedulerTest {

    private lateinit var ctx: Context
    private lateinit var wm: WorkManager

    @Before
    fun setUp() {
        ctx = ApplicationProvider.getApplicationContext()
        // SynchronousExecutor so getWorkInfosForUniqueWork().get() resolves on
        // the calling thread. The test never marks delays/period met, so no
        // enqueued worker actually executes.
        val config = Configuration.Builder()
            .setExecutor(SynchronousExecutor())
            .build()
        WorkManagerTestInitHelper.initializeTestWorkManager(ctx, config)
        wm = WorkManager.getInstance(ctx)
    }

    // True if the named unique work is scheduled (ENQUEUED or RUNNING). A
    // cancelled or never-enqueued name reports false.
    private fun isArmed(name: String): Boolean =
        wm.getWorkInfosForUniqueWork(name).get()
            .any { it.state == WorkInfo.State.ENQUEUED || it.state == WorkInfo.State.RUNNING }

    @Test
    fun periodicArmsOnlyThePeriodicWork() {
        SyncScheduler.enqueueFromPlan(ctx, WakePlan("periodic", 120, 30, emptyList()))

        assertTrue(isArmed(PowerLogic.WORK_PERIODIC))
        assertFalse(isArmed(PowerLogic.WORK_POLL))
        assertFalse(isArmed(PowerLogic.WORK_SAFETY))
        assertFalse(isArmed(PowerLogic.WORK_SCHEDULED))
    }

    @Test
    fun onChangePollArmsPollAndSafetyNet() {
        SyncScheduler.enqueueFromPlan(ctx, WakePlan("on_change_poll", 240, 30, emptyList()))

        assertTrue(isArmed(PowerLogic.WORK_POLL))
        assertTrue(isArmed(PowerLogic.WORK_SAFETY))
        assertFalse(isArmed(PowerLogic.WORK_PERIODIC))
        assertFalse(isArmed(PowerLogic.WORK_SCHEDULED))
    }

    @Test
    fun scheduledArmsTheOneTimeScheduledWork() {
        SyncScheduler.enqueueFromPlan(ctx, WakePlan("scheduled", 480, 30, listOf("23:59")))

        assertTrue(isArmed(PowerLogic.WORK_SCHEDULED))
        assertFalse(isArmed(PowerLogic.WORK_PERIODIC))
        assertFalse(isArmed(PowerLogic.WORK_POLL))
        assertFalse(isArmed(PowerLogic.WORK_SAFETY))
    }

    @Test
    fun modeSwitchCancelsStaleWork() {
        // Start on the two-work on_change_poll mode...
        SyncScheduler.enqueueFromPlan(ctx, WakePlan("on_change_poll", 240, 30, emptyList()))
        assertTrue(isArmed(PowerLogic.WORK_POLL))
        assertTrue(isArmed(PowerLogic.WORK_SAFETY))

        // ...then switch to periodic. The poll + safety work must be cancelled,
        // not left running alongside the new periodic work.
        SyncScheduler.enqueueFromPlan(ctx, WakePlan("periodic", 120, 30, emptyList()))

        assertTrue(isArmed(PowerLogic.WORK_PERIODIC))
        assertFalse(isArmed(PowerLogic.WORK_POLL))
        assertFalse(isArmed(PowerLogic.WORK_SAFETY))
    }

    @Test
    fun emptyPlanCancelsEverything() {
        SyncScheduler.enqueueFromPlan(ctx, WakePlan("periodic", 120, 30, emptyList()))
        assertTrue(isArmed(PowerLogic.WORK_PERIODIC))

        // Unknown/empty mode → planWorks yields nothing → every sync work cancelled.
        SyncScheduler.enqueueFromPlan(ctx, WakePlan("", 120, 30, emptyList()))

        PowerLogic.ALL_SYNC_WORK.forEach { assertFalse(isArmed(it)) }
    }
}
