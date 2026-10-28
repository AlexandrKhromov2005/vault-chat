package io.github.alexandrkhromov2005.vaultchat.core.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabases
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
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

    @After
    fun tearDown() {
        scope.cancel()
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
