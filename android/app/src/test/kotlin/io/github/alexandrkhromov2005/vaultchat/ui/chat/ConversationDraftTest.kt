package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.net.Uri
import android.os.Looper
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSession
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSessionDeps
import io.github.alexandrkhromov2005.vaultchat.core.chat.FakeChatServer
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileKind
import io.github.alexandrkhromov2005.vaultchat.core.chat.SessionFiles
import io.github.alexandrkhromov2005.vaultchat.core.chat.Staging
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import retrofit2.Response
import java.io.File
import java.io.IOException

/** Files picked for the next message (docs/android/attachments.md, "Sending"). */
@RunWith(RobolectricTestRunner::class)
class ConversationDraftTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val server = FakeChatServer(me)
    private lateinit var database: ChatDatabase
    private lateinit var chat: ChatSession
    private lateinit var staging: File

    // Nothing is sent: there is no network.
    private val deps = object : ChatSessionDeps {
        override val foreground: StateFlow<Boolean> = MutableStateFlow(false)
        override val network: StateFlow<Any?> = MutableStateFlow(null)
        override val elapsed: () -> Long = { System.nanoTime() / 1_000_000 }

        override suspend fun <T : Any> send(session: SessionState.SignedIn, request: suspend GatewayApi.(String) -> Response<T>): T =
            server.requests.send(request)

        override suspend fun grant(session: SessionState.SignedIn, minValidityMs: Long): AccessGrant = error("no socket")

        override suspend fun renew(session: SessionState.SignedIn, rejected: String): AccessGrant = error("no socket")

        override fun openSocket(session: SessionState.SignedIn, listener: WebSocketListener): WebSocket = error("no socket")
    }

    @get:Rule
    val temporary = TemporaryFolder()

    private val picked = mutableMapOf<Uri, PickedFile>()
    private val store = ViewModelStore()
    private lateinit var roomId: String

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        val signedIn = SessionState.SignedIn(ServerAddress.parse("https://chat.example.org", allowLocalCleartext = false), me, "me")
        staging = temporary.newFolder("staging")
        chat = ChatSession(signedIn, "test.db", database, SessionFiles(temporary.newFolder("cache"), staging), deps, onReleased = {})
        roomId = server.addChannel(1)
        runBlocking { chat.sync.syncNow() }
    }

    @After
    fun tearDown() {
        store.clear()
        chat.end(background = CoroutineScope(Dispatchers.IO))
        database.close()
    }

    private fun model(): ConversationViewModel = ViewModelProvider.create(
        store,
        viewModelFactory { initializer { ConversationViewModel(chat, roomId) { uri -> picked.getValue(uri) } } },
    )[ConversationViewModel::class.java]

    private fun pick(name: String, content: ByteArray): Uri {
        val uri = Uri.parse("content://picker/${picked.size}")
        picked[uri] = PickedFile(name) { content.inputStream() }
        return uri
    }

    private fun settle(condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + 5_000
        while (!condition() && System.currentTimeMillis() < deadline) {
            shadowOf(Looper.getMainLooper()).idle()
            Thread.sleep(10)
        }
        shadowOf(Looper.getMainLooper()).idle()
    }

    private fun staged() = staging.list().orEmpty().toSet()

    @Test
    fun filesStoredForAnotherRoomAreNotReadWithAMessage() = runBlocking {
        val fileId = server.id(5, 1)
        database.dao().storeFile(FileEntity(fileId, roomId, me, "a.txt", "text/plain", 1, 0))
        assertEquals(setOf(fileId), chat.storedFiles(roomId, listOf(fileId)).keys)
        assertEquals(emptySet<String>(), chat.storedFiles(server.id(1, 2), listOf(fileId)).keys)
    }

    @Test
    fun pickedFilesAreCheckedAndAtMostFiveAreKept() {
        val model = model()
        val notices = mutableListOf<DraftNotice>()
        CoroutineScope(Dispatchers.Main).launch { model.notices.collect { notices += it } }
        shadowOf(Looper.getMainLooper()).idle()

        val heic = byteArrayOf(0, 0, 0, 0x18, 0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x63)
        model.attach(listOf(pick("a.txt", "a".toByteArray()), pick("photo.heic", heic), pick("empty.txt", ByteArray(0))))
        settle { model.staging.value == 0 && notices.size == 2 }
        assertEquals(listOf("a.txt"), model.draft.value.map { it.name })
        assertEquals(FileKind.TEXT, model.draft.value.single().kind)
        assertEquals(
            listOf(DraftNotice("photo.heic", Staging.Problem.UNSUPPORTED), DraftNotice("empty.txt", Staging.Problem.EMPTY)),
            notices,
        )

        // Five at most: four more fit, the rest is refused with one notice.
        model.attach((1..6).map { pick("$it.txt", "$it".toByteArray()) })
        settle { model.staging.value == 0 && model.draft.value.size == 5 }
        // In the order they were picked, which is the order of attachment_ids.
        assertEquals(listOf("a.txt", "1.txt", "2.txt", "3.txt", "4.txt"), model.draft.value.map { it.name })
        settle { notices.size == 3 }
        assertEquals(DraftNotice(null, null), notices.last())
        assertEquals(5, staged().size)
    }

    @Test
    fun anUnreadableFileIsReported() {
        val model = model()
        val notices = mutableListOf<DraftNotice>()
        CoroutineScope(Dispatchers.Main).launch { model.notices.collect { notices += it } }
        shadowOf(Looper.getMainLooper()).idle()
        val uri = Uri.parse("content://picker/gone")
        picked[uri] = PickedFile("gone.txt") { throw IOException("revoked") }
        model.attach(listOf(uri))
        settle { notices.isNotEmpty() }
        assertEquals(listOf(DraftNotice("gone.txt", Staging.Problem.UNREADABLE)), notices)
    }

    @Test
    fun aRemovedFileIsDeleted() {
        val model = model()
        model.attach(listOf(pick("a.txt", "a".toByteArray()), pick("b.txt", "b".toByteArray())))
        settle { model.draft.value.size == 2 }
        val removed = model.draft.value.first { it.name == "a.txt" }
        model.detach(removed.staged)
        assertEquals(listOf("b.txt"), model.draft.value.map { it.name })
        assertTrue(removed.staged !in staged())
    }

    @Test
    fun sentFilesBelongToTheQueueAndAnUnsentDraftIsDeletedWithTheScreen() {
        val model = model()
        model.attach(listOf(pick("sent.txt", "s".toByteArray())))
        settle { model.draft.value.size == 1 }
        val sent = model.draft.value.single()
        var queued: Boolean? = null
        model.send("") { queued = it }
        settle { queued != null }
        assertEquals(true, queued)
        assertEquals(emptyList<Any>(), model.draft.value)
        val rows = runBlocking { database.dao().storedOutgoing(roomId) }
        assertEquals("", rows.single().text)
        assertEquals(listOf(sent.staged), runBlocking { database.dao().filesOf(rows.single().clientMessageId) }.map { it.staged })

        model.attach(listOf(pick("draft.txt", "d".toByteArray())))
        settle { model.draft.value.size == 1 }
        val draft = model.draft.value.single()
        store.clear()
        // The queued copy stays for its upload; the draft goes.
        assertEquals(setOf(sent.staged), staged())
        assertTrue(draft.staged !in staged())
    }
}
