package io.github.alexandrkhromov2005.vaultchat.core.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabases
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import retrofit2.Response
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import kotlin.coroutines.CoroutineContext

@RunWith(RobolectricTestRunner::class)
class ChatSessionsTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val databases = ChatDatabases(context)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val server = ServerAddress.parse("https://chat.example.org", allowLocalCleartext = false)
    private val alice = SessionState.SignedIn(server, "00000000-0000-4000-8000-0000000000aa", "alice")
    private val fileName = ChatDatabases.fileName(server.toString(), alice.userId)

    // Nothing reaches the network: the app is never in the foreground.
    private val deps = object : ChatSessionDeps {
        override val foreground: StateFlow<Boolean> = MutableStateFlow(false)
        override val network: StateFlow<Any?> = MutableStateFlow(null)
        override val elapsed: () -> Long = System::currentTimeMillis

        override suspend fun <T : Any> send(session: SessionState.SignedIn, request: suspend GatewayApi.(String) -> Response<T>): T =
            throw UnsupportedOperationException()

        override suspend fun grant(session: SessionState.SignedIn, minValidityMs: Long): AccessGrant =
            throw UnsupportedOperationException()

        override suspend fun renew(session: SessionState.SignedIn, rejected: String): AccessGrant =
            throw UnsupportedOperationException()

        override fun openSocket(session: SessionState.SignedIn, listener: WebSocketListener): WebSocket =
            throw UnsupportedOperationException()
    }

    // A session in the background with a network, as the background job finds it.
    private fun backgroundDeps(gateway: FakeChatServer) = object : ChatSessionDeps by deps {
        override val network: StateFlow<Any?> = MutableStateFlow(Unit)

        override suspend fun <T : Any> send(session: SessionState.SignedIn, request: suspend GatewayApi.(String) -> Response<T>): T =
            gateway.requests.send(request)
    }

    private val io = HeldDispatcher()

    @After
    fun tearDown() {
        io.close()
        scope.cancel()
    }

    /** What the database holds for the current session, read through a handle of its own. */
    private suspend fun storedUnsentTexts(roomId: String): List<String> {
        assertTrue("the database file was deleted", fileName in context.databaseList())
        val database = databases.open(server.toString(), alice.userId)
        return try {
            database.dao().storedOutgoing(roomId).map { it.text }
        } finally {
            database.close()
        }
    }

    /** Leaves a queued message behind, as a process killed after queuing it would. */
    private suspend fun queueAMessage(gateway: FakeChatServer): String {
        val roomId = gateway.addChannel(1)
        val database = databases.open(server.toString(), alice.userId)
        val dao = database.dao()
        dao.storeRoomList(listOf(RoomEntity(roomId, RoomEntity.CHANNEL, "general", null, false, 0, null, null, 0)), complete = true)
        dao.enqueue(OutgoingEntity(gateway.id(4, 1), roomId, alice.userId, 0, "hello", 0, OutgoingEntity.QUEUED))
        database.close()
        return roomId
    }

    private suspend fun storeARoom() {
        val database = databases.open(server.toString(), alice.userId)
        val room = RoomEntity("room-1", RoomEntity.CHANNEL, "general", null, false, 0, null, null, 0)
        database.dao().storeRoomList(listOf(room), complete = true)
        database.close()
    }

    @Test
    fun theStoredHistoryIsKeptWhileTheSessionIsRestored() = runBlocking {
        storeARoom()
        val state = MutableStateFlow<SessionState>(SessionState.Restoring)
        val sessions = ChatSessions(databases, deps, scope)
        sessions.follow(state)
        // Give a clean-up during restore time to run.
        delay(500)

        state.value = alice
        val chat = sessions.get(alice)

        assertEquals(listOf("room-1"), chat.rooms.first().map { it.room.roomId })
    }

    @Test
    fun signingOutDeletesTheDataAndTheFile() = runBlocking {
        storeARoom()
        val state = MutableStateFlow<SessionState>(alice)
        val sessions = ChatSessions(databases, deps, scope)
        sessions.follow(state)
        val chat = sessions.get(alice)
        val screen = chat.retain()

        state.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        withTimeout(5_000) { while (chat.rooms.first().isNotEmpty()) delay(10) }
        assertTrue(fileName in context.databaseList())

        // Gone once no screen reads from it any more.
        screen.close()
        withTimeout(10_000) { while (fileName in context.databaseList()) delay(50) }
        assertFalse(fileName in context.databaseList())
    }

    @Test
    fun signingOutDeletesTheFilesAtOnce() = runBlocking {
        val files = databases.files(fileName)
        val state = MutableStateFlow<SessionState>(alice)
        val sessions = ChatSessions(databases, deps, scope)
        sessions.follow(state)
        val screen = sessions.get(alice).retain()
        File(files.cache, "picture").apply { parentFile!!.mkdirs() }.writeText("downloaded")
        File(files.staging, "picked").apply { parentFile!!.mkdirs() }.writeText("waiting to be sent")

        state.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        // Even while a screen still reads from the database.
        withTimeout(5_000) { while (files.cache.exists() || files.staging.exists()) delay(10) }
        screen.close()
    }

    @Test
    fun filesOfSessionsThatAreNotOpenAreSwept() = runBlocking {
        // Left behind by a killed process.
        val stale = databases.files(ChatDatabases.fileName(server.toString(), "00000000-0000-4000-8000-0000000000bb"))
        stale.cache.mkdirs()
        stale.staging.mkdirs()
        val mine = databases.files(fileName)
        val sessions = ChatSessions(databases, deps, scope)
        sessions.follow(MutableStateFlow(alice))
        sessions.get(alice)
        File(mine.cache, "picture").apply { parentFile!!.mkdirs() }.writeText("downloaded")

        withTimeout(5_000) { while (stale.cache.exists() || stale.staging.exists()) delay(10) }
        assertTrue(File(mine.cache, "picture").exists())
    }

    @Test
    fun aCleanUpQueuedWhileSignedOutLeavesTheDatabaseOfANewSession() = runBlocking {
        val cleanUps = WatchedDatabases(context)
        val state = MutableStateFlow<SessionState>(SessionState.SignedOut(SessionState.Reason.NONE))
        val sessions = ChatSessions(cleanUps, deps, scope, io)
        sessions.follow(state)
        // The clean-up for "nobody is signed in" waits for its turn.
        io.awaitQueued(1)

        state.value = alice
        sessions.get(alice).rooms.first()
        val roomId = queueAMessage(FakeChatServer(alice.userId))
        io.release()
        io.awaitIdle()

        // Asked of the clean-up as well as of the file: Windows refuses to
        // delete an open file, where Android and Linux delete it.
        assertTrue(cleanUps.kept.isNotEmpty())
        assertTrue("a clean-up left out the open database: ${cleanUps.kept}", cleanUps.kept.all { fileName in it })
        assertEquals(listOf("hello"), storedUnsentTexts(roomId))
    }

    @Test
    fun aSessionOpensOnlyAfterACleanUpUnderWayHasFinished() = runBlocking {
        val cleanUps = WatchedDatabases(context, hold = true)
        val state = MutableStateFlow<SessionState>(SessionState.SignedOut(SessionState.Reason.NONE))
        val sessions = ChatSessions(cleanUps, deps, scope, io)
        sessions.follow(state)
        io.release()
        // The clean-up is deleting files for a set it worked out before alice came.
        cleanUps.awaitDeleting()

        state.value = alice
        val opening = scope.async { sessions.get(alice) }
        delay(300)

        assertFalse("the session opened during the clean-up", opening.isCompleted)
        cleanUps.letDeletingFinish()
        withTimeout(5_000) { opening.await() }
        Unit
    }

    @Test
    fun aWipeQueuedAtSignOutLeavesTheDataOfTheSameUsersNewSession() = runBlocking {
        val state = MutableStateFlow<SessionState>(alice)
        val sessions = ChatSessions(databases, deps, scope, io)
        sessions.follow(state)
        sessions.get(alice).rooms.first()
        io.awaitQueued(1)
        state.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        // The wipe of the ended session and a clean-up wait for their turn.
        io.awaitQueued(3)

        val again = SessionState.SignedIn(server, alice.userId, alice.username)
        state.value = again
        sessions.get(again).rooms.first()
        val roomId = queueAMessage(FakeChatServer(alice.userId))
        io.release()
        // Past the ended session's close and the clean-up after it.
        delay(2_500)
        io.awaitIdle()

        assertEquals(listOf("hello"), storedUnsentTexts(roomId))
    }

    @Test
    fun theBackgroundJobSendsTheQueueOfTheStoredSession() = runBlocking {
        val gateway = FakeChatServer(alice.userId)
        val roomId = queueAMessage(gateway)
        val state = MutableStateFlow<SessionState>(SessionState.Restoring)
        val sessions = ChatSessions(databases, backgroundDeps(gateway), scope)
        sessions.follow(state)

        val job = scope.async { sendQueuedMessages(sessions, budgetMs = 5_000) }
        delay(200)
        // The job waits for the stored session to be known.
        assertFalse(job.isCompleted)
        state.value = alice

        assertTrue(job.await())
        assertEquals(listOf("hello"), gateway.sent(roomId).map { it.text })
    }

    @Test
    fun theBackgroundJobAsksForAnotherRunWhenItCannotSendInTime() = runBlocking {
        val gateway = FakeChatServer(alice.userId)
        val roomId = queueAMessage(gateway)
        gateway.intercept = { if (it == "send $roomId") 503 else null }
        val sessions = ChatSessions(databases, backgroundDeps(gateway), scope)
        sessions.follow(MutableStateFlow(alice))

        assertFalse(sendQueuedMessages(sessions, budgetMs = 500))
    }

    @Test
    fun theBackgroundJobKeepsToItsBudgetWhileTheSessionIsRestored() = runBlocking {
        val sessions = ChatSessions(databases, deps, scope)
        sessions.follow(MutableStateFlow(SessionState.Restoring))

        // A restore that never ends must not hold the job until WorkManager stops it.
        assertFalse(withTimeout(2_000) { sendQueuedMessages(sessions, budgetMs = 300) })
    }

    @Test
    fun theBackgroundJobEndsAtOnceWithoutASession() = runBlocking {
        val sessions = ChatSessions(databases, deps, scope)
        sessions.follow(MutableStateFlow(SessionState.SignedOut(SessionState.Reason.NONE)))

        assertTrue(withTimeout(1_000) { sendQueuedMessages(sessions, budgetMs = 60_000) })
    }

    /** Records what is posted; [posting] holds a post under way until it is released. */
    private class WatchedBoard : NoticeBoard {
        val shown: MutableSet<String> = java.util.Collections.synchronizedSet(mutableSetOf())
        var entered = CountDownLatch(0)
        var release = CountDownLatch(0)

        override fun post(account: String, notices: List<RoomNotice>) {
            entered.countDown()
            release.await()
            shown += notices.map { it.roomId }
        }

        override fun shownRooms(): Set<String> = shown.toSet()

        override fun cancel(roomId: String) {
            shown -= roomId
        }
    }

    /** Stores an unread message of someone else, as a sync would. */
    private suspend fun leaveAnUnshownMessage(): String {
        val roomId = "00000000-0000-4000-8000-000000000001"
        val database = databases.open(server.toString(), alice.userId)
        val dao = database.dao()
        dao.storeRoomList(listOf(RoomEntity(roomId, RoomEntity.CHANNEL, "general", null, false, 0, null, null, 0)), complete = true)
        val message = MessageEntity(roomId, 1, "m-1", "00000000-0000-4000-8000-0000000000bb", "c-1", "synthetic", attachments = "", createdAt = 1)
        dao.applyLatest(roomId, listOf(message), readThrough = 0)
        database.close()
        return roomId
    }

    @Test
    fun anEndedSessionPostsNothing() = runBlocking {
        val board = WatchedBoard()
        val noticing = object : ChatSessionDeps by deps {
            override val notices: NoticeBoard = board
        }
        val sessions = ChatSessions(databases, noticing, scope)
        sessions.follow(MutableStateFlow(alice))
        val chat = sessions.get(alice)
        val screen = chat.retain()

        // As when the background job's sync failed because the session ended,
        // and the wipe has not run yet.
        chat.end(scope)
        leaveAnUnshownMessage()
        chat.notifier.check()

        assertEquals(emptySet<String>(), board.shown.toSet())
        screen.close()
    }

    @Test
    fun aPostUnderWayAtSignOutDoesNotOutliveTheCleanUp() = runBlocking {
        val board = WatchedBoard()
        val noticing = object : ChatSessionDeps by deps {
            override val notices: NoticeBoard = board

            override fun accountEnded() {
                board.shown.clear()
            }
        }
        board.entered = CountDownLatch(1)
        board.release = CountDownLatch(1)
        leaveAnUnshownMessage()
        val state = MutableStateFlow<SessionState>(alice)
        val sessions = ChatSessions(databases, noticing, scope)
        sessions.follow(state)
        val chat = sessions.get(alice)
        val screen = chat.retain()
        withTimeout(5_000) { while (board.entered.count > 0) delay(10) }

        // The post has its notice and is about to show it.
        state.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        delay(300)
        board.release.countDown()
        delay(500)

        assertEquals("a notice appeared after the clean-up", emptySet<String>(), board.shown.toSet())
        screen.close()
    }

    @Test
    fun aScreenLateForAnEndedSessionDoesNotRestartIt() = runBlocking {
        val state = MutableStateFlow<SessionState>(alice)
        val sessions = ChatSessions(databases, deps, scope)
        sessions.follow(state)
        val chat = sessions.get(alice)
        state.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        val bob = SessionState.SignedIn(server, "00000000-0000-4000-8000-0000000000bb", "bob")
        state.value = bob
        val current = sessions.get(bob)

        // A screen still composed for alice asks again.
        val late = sessions.get(alice)

        assertNotSame(chat, late)
        assertTrue(sessions.get(bob) === current)
    }
}

/** Records what clean-ups keep; with [hold], the first one stops before it deletes. */
private class WatchedDatabases(context: Context, private val hold: Boolean = false) : ChatDatabases(context) {
    val kept = java.util.Collections.synchronizedList(mutableListOf<Set<String>>())
    private val deleting = CountDownLatch(1)
    private val finish = CountDownLatch(1)

    override fun deleteAllExcept(keep: Set<String>) {
        kept += keep
        deleting.countDown()
        if (hold) finish.await()
        super.deleteAllExcept(keep)
    }

    suspend fun awaitDeleting() = withTimeout(5_000) {
        while (deleting.count > 0) delay(10)
    }

    fun letDeletingFinish() = finish.countDown()
}

/** One worker that runs nothing until [release], so that work piles up in a known order. */
private class HeldDispatcher : CoroutineDispatcher() {
    private val worker = Executors.newSingleThreadExecutor()
    private val gate = CountDownLatch(1)
    private val queued = AtomicInteger()
    private val done = AtomicInteger()

    init {
        worker.execute { gate.await() }
    }

    override fun dispatch(context: CoroutineContext, block: Runnable) {
        queued.incrementAndGet()
        worker.execute {
            try {
                block.run()
            } finally {
                done.incrementAndGet()
            }
        }
    }

    suspend fun awaitQueued(count: Int) = withTimeout(5_000) {
        while (queued.get() < count) delay(10)
    }

    fun release() = gate.countDown()

    /** Until everything queued has run and nothing new has come for a while. */
    suspend fun awaitIdle() = withTimeout(10_000) {
        var seen = -1
        while (seen != queued.get() || done.get() != seen) {
            seen = queued.get()
            delay(300)
        }
    }

    fun close() {
        release()
        worker.shutdownNow()
    }
}
