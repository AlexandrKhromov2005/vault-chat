package io.github.alexandrkhromov2005.vaultchat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class OutboxWorkerTest {
    private val context: Context = ApplicationProvider.getApplicationContext()

    @Before
    fun setUp() {
        WorkManagerTestInitHelper.initializeTestWorkManager(context, Configuration.Builder().setExecutor(SynchronousExecutor()).build())
    }

    private fun jobs(): List<WorkInfo> = WorkManager.getInstance(context).getWorkInfosForUniqueWork(OutboxWorker.NAME).get()

    /**
     * A job that waits (for a network, or for WorkManager's backoff after
     * retries) or that is about to end is replaced by a new one when a message
     * is queued, so that the new message is not left to the old job's timing.
     */
    @Test
    fun queuingAgainReplacesTheWaitingJob() {
        OutboxWorker.schedule(context)
        val first = jobs().single()
        assertEquals(WorkInfo.State.ENQUEUED, first.state)

        OutboxWorker.schedule(context)

        val waiting = jobs().filter { it.state == WorkInfo.State.ENQUEUED }
        assertEquals(1, waiting.size)
        assertNotEquals(first.id, waiting.single().id)
    }
}
