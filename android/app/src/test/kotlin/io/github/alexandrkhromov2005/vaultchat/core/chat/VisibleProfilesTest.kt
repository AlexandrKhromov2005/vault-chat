package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import kotlin.coroutines.CoroutineContext

/** Names read for the rows on screen (ChatSync.followVisibleProfiles). */
@RunWith(RobolectricTestRunner::class)
class VisibleProfilesTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val bob = "00000000-0000-4000-8000-0000000000bb"
    private val server = FakeChatServer(me)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        dao = database.dao()
        server.profiles[bob] = "bob"
    }

    @After
    fun tearDown() {
        scope.cancel()
        database.close()
    }

    /** Runs dispatched tasks only when the test says so, like a busy main thread. */
    private class StepDispatcher : CoroutineDispatcher() {
        val tasks = LinkedBlockingQueue<Runnable>()

        override fun dispatch(context: CoroutineContext, block: Runnable) {
            tasks.add(block)
        }
    }

    /**
     * followVisibleProfiles adds a user to profilesInFlight and then
     * launches the request, whose finally removes it. A coroutine cancelled
     * before it starts never runs its body, finally included. If the screen
     * goes away (or the rows change) after the add but before the launched
     * request has started, the user stays "in flight" for the rest of the
     * session: neither the screen nor background loadProfiles asks for the
     * name again, and the row shows "unknown" until the app restarts.
     */
    @Test
    fun aNameWhoseRequestWasCancelledBeforeItStartedIsReadLater() = runBlocking {
        val sync = ChatSync(dao, me, server.requests, scope, System::currentTimeMillis)
        val inFlight = ChatSync::class.java.getDeclaredField("profilesInFlight").apply { isAccessible = true }
            .get(sync) as MutableSet<*>

        val main = StepDispatcher()
        val screen = CoroutineScope(main).launch { sync.followVisibleProfiles(flowOf(listOf(bob))) }
        // Run the screen's tasks one by one until bob is marked in flight;
        // his request is then queued but has not started.
        while (bob !in inFlight) {
            val task = main.tasks.poll(5, TimeUnit.SECONDS) ?: error("bob was never marked in flight")
            task.run()
        }
        screen.cancel()
        while (true) (main.tasks.poll(200, TimeUnit.MILLISECONDS) ?: break).run()
        assertTrue("the screen's work ended", screen.isCompleted)

        // The screen opens again with bob on screen.
        withTimeoutOrNull(2_000) { sync.followVisibleProfiles(flowOf(listOf(bob))) }
        assertEquals("bob's name was read", listOf(bob), dao.knownProfiles(listOf(bob)))
    }

    /**
     * followVisibleProfiles now runs in the members screen's LaunchedEffect,
     * not in the session scope whose handler swallowed failures. A database
     * failure (knownProfiles, upsertProfiles) escapes it and, in a
     * LaunchedEffect, crashes the app; rooms.md says "Database failure in
     * any of these actions: nothing crashes".
     */
    @Test
    fun aDatabaseFailureWhileReadingNamesDoesNotEscape() = runBlocking {
        val sync = ChatSync(dao, me, server.requests, scope, System::currentTimeMillis)
        database.openHelper.writableDatabase.execSQL("ALTER TABLE profiles RENAME TO profiles_unavailable")
        val thrown = try {
            withTimeoutOrNull(2_000) { sync.followVisibleProfiles(flowOf(listOf(bob))) }
            null
        } catch (e: Exception) {
            e
        } finally {
            database.openHelper.writableDatabase.execSQL("ALTER TABLE profiles_unavailable RENAME TO profiles")
        }
        assertEquals("escaped: $thrown", null, thrown)
    }
}
