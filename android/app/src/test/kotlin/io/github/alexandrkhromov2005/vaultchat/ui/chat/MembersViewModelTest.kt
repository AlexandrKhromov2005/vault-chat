package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.os.Looper
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSession
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSessionDeps
import io.github.alexandrkhromov2005.vaultchat.core.chat.FakeChatServer
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import retrofit2.Response
import java.util.concurrent.atomic.AtomicBoolean

@RunWith(RobolectricTestRunner::class)
class MembersViewModelTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val bob = "00000000-0000-4000-8000-0000000000bb"
    private val carol = "00000000-0000-4000-8000-0000000000cc"
    private val server = FakeChatServer(me)
    private lateinit var database: ChatDatabase
    private lateinit var chat: ChatSession

    private val deps = object : ChatSessionDeps {
        override val foreground: StateFlow<Boolean> = MutableStateFlow(false)
        override val network: StateFlow<Any?> = MutableStateFlow(null)
        override val elapsed: () -> Long = { System.nanoTime() / 1_000_000 }

        override suspend fun <T : Any> send(session: SessionState.SignedIn, request: suspend GatewayApi.(String) -> Response<T>): T =
            // Off the main thread, as over the network.
            withContext(Dispatchers.IO) { server.requests.send(request) }

        override suspend fun grant(session: SessionState.SignedIn, minValidityMs: Long): AccessGrant = error("no socket")

        override suspend fun renew(session: SessionState.SignedIn, rejected: String): AccessGrant = error("no socket")

        override fun openSocket(session: SessionState.SignedIn, listener: WebSocketListener): WebSocket = error("no socket")
    }

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        val signedIn = SessionState.SignedIn(ServerAddress.parse("https://chat.example.org", allowLocalCleartext = false), me, "me")
        chat = ChatSession(signedIn, "test.db", database, deps, onReleased = {})
    }

    @After
    fun tearDown() {
        chat.end(wipe = false, background = CoroutineScope(Dispatchers.IO))
        database.close()
    }

    private fun settle(condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + 5_000
        while (!condition() && System.currentTimeMillis() < deadline) {
            shadowOf(Looper.getMainLooper()).idle()
            Thread.sleep(10)
        }
        shadowOf(Looper.getMainLooper()).idle()
    }

    /**
     * The screen reads its roster again after every sync, since member
     * events may have been lost while the WebSocket was down. A sync that
     * ends while a read is under way calls reload(), which returns at once
     * because `loading` is set: the read under way may predate the change,
     * and nothing reads again until the next sync. Here carol joins and a
     * sync completes while the first page is being answered, with her event
     * lost; the screen keeps showing the roster without her.
     */
    @Test
    fun aSyncThatEndsDuringARosterReadIsNotDropped() {
        val room = server.addChannel(1)
        server.join(room, bob)
        assertNull(runBlocking { chat.sync.syncNow() })

        val once = AtomicBoolean(true)
        server.afterMemberPage = {
            if (once.compareAndSet(true, false)) {
                // The page is already prepared without carol.
                server.join(room, carol)
                // The screen has subscribed to completed by now.
                Thread.sleep(200)
                assertNull(runBlocking { chat.sync.syncNow() })
            }
        }
        val model = MembersViewModel(chat, room)
        settle { !model.loading.value && runBlocking { database.dao().memberIds(room) }.contains(carol) }

        assertEquals(listOf(me, bob, carol).sorted(), runBlocking { database.dao().memberIds(room) })
    }
}
