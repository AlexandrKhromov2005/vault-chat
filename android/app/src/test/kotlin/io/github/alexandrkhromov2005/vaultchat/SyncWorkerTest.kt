package io.github.alexandrkhromov2005.vaultchat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.ExistingWorkPolicy
import androidx.work.ListenableWorker
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerFactory
import androidx.work.WorkerParameters
import androidx.work.testing.TestListenableWorkerBuilder
import androidx.work.testing.WorkManagerTestInitHelper
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
class SyncWorkerTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val started = AtomicInteger()
    private val stopped = AtomicBoolean()
    private val finish = CountDownLatch(1)
    private val executor = Executors.newCachedThreadPool()

    // A sync that runs until the test lets it end.
    private val held = object : WorkerFactory() {
        override fun createWorker(appContext: Context, workerClassName: String, workerParameters: WorkerParameters): ListenableWorker =
            object : Worker(appContext, workerParameters) {
                override fun doWork(): Result {
                    started.incrementAndGet()
                    finish.await(20, TimeUnit.SECONDS)
                    return Result.success()
                }

                override fun onStopped() {
                    stopped.set(true)
                }
            }
    }

    private fun manager(): WorkManager {
        val configuration = Configuration.Builder().setWorkerFactory(held).setExecutor(executor).build()
        WorkManagerTestInitHelper.initializeTestWorkManager(context, configuration)
        return WorkManager.getInstance(context)
    }

    @After
    fun tearDown() {
        finish.countDown()
        executor.shutdownNow()
    }

    private fun jobs(manager: WorkManager): List<WorkInfo> = manager.getWorkInfosForUniqueWork(SyncWorker.NOW).get()

    private fun waitFor(what: String, condition: () -> Boolean) {
        repeat(250) {
            if (condition()) return
            Thread.sleep(20)
        }
        assertTrue(what, condition())
    }

    private fun letRun(job: WorkInfo) = WorkManagerTestInitHelper.getTestDriver(context)!!.setAllConstraintsMet(job.id)

    @Test
    fun aPushDuringASyncLetsItFinishAndRunsOneMoreAfterIt() {
        val manager = manager()
        SyncWorker.runNow(context)
        val first = jobs(manager).single()
        letRun(first)
        waitFor("the sync never started") { started.get() == 1 }

        // Pushes that arrive after the running sync has passed the server.
        repeat(3) { SyncWorker.runNow(context) }

        assertFalse("the running sync was cancelled", stopped.get())
        val states = jobs(manager).map { it.state }
        assertEquals(1, states.count { it == WorkInfo.State.RUNNING })
        assertEquals("not exactly one sync waits: $states", 1, states.count { it == WorkInfo.State.BLOCKED })

        finish.countDown()
        waitFor("the first sync never ended") { jobs(manager).first { it.id == first.id }.state == WorkInfo.State.SUCCEEDED }
        val next = jobs(manager).single { it.id != first.id && !it.state.isFinished }
        letRun(next)
        waitFor("the push after the first sync was lost") { started.get() == 2 }
    }

    @Test
    fun aPushReplacesASyncThatHasNotStarted() {
        val manager = manager()
        SyncWorker.runNow(context)
        val first = jobs(manager).single()

        SyncWorker.runNow(context)

        val waiting = jobs(manager).filterNot { it.state.isFinished }
        assertEquals(1, waiting.size)
        assertTrue("the waiting sync was not replaced", waiting.single().id != first.id)
    }

    @Test
    fun whatAPushDoesDependsOnlyOnTheSyncsUnderWay() {
        val policy = SyncWorker::policyFor
        // Nothing, or nothing running: a fresh sync, without a backoff of an earlier one.
        assertEquals(ExistingWorkPolicy.REPLACE, policy(emptyList()))
        assertEquals(ExistingWorkPolicy.REPLACE, policy(listOf(WorkInfo.State.SUCCEEDED)))
        assertEquals(ExistingWorkPolicy.REPLACE, policy(listOf(WorkInfo.State.ENQUEUED)))
        assertEquals(ExistingWorkPolicy.REPLACE, policy(listOf(WorkInfo.State.ENQUEUED, WorkInfo.State.BLOCKED)))
        // Running: one more after it, since it may have passed the server already.
        assertEquals(ExistingWorkPolicy.APPEND_OR_REPLACE, policy(listOf(WorkInfo.State.RUNNING)))
        assertEquals(ExistingWorkPolicy.APPEND_OR_REPLACE, policy(listOf(WorkInfo.State.SUCCEEDED, WorkInfo.State.RUNNING)))
        // One more is already waiting and will see the message.
        assertNull(policy(listOf(WorkInfo.State.RUNNING, WorkInfo.State.BLOCKED)))
    }

    @Test
    fun aSyncWithNobodySignedInSucceedsAtOnce() = runBlocking {
        val worker = TestListenableWorkerBuilder<SyncWorker>(context).build()

        assertEquals(ListenableWorker.Result.success(), worker.doWork())
    }
}
