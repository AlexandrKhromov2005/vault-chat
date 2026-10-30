package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.Collections

@RunWith(RobolectricTestRunner::class)
class NotifierTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val bob = "00000000-0000-4000-8000-0000000000bb"
    private val server = FakeChatServer(me)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val foreground = MutableStateFlow(false)
    private val posted = Collections.synchronizedList(mutableListOf<List<RoomNotice>>())
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao
    private lateinit var sync: ChatSync

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        dao = database.dao()
        sync = ChatSync(dao, me, server.requests, scope, System::currentTimeMillis)
    }

    @After
    fun tearDown() {
        scope.cancel()
        database.close()
    }

    // Checked explicitly, as the background sync does; the collector of changes does the same.
    private fun notifier() = Notifier(dao, me, foreground, { posted += it }, CoroutineScope(Dispatchers.Unconfined + SupervisorJob()).also { it.cancel() })

    private fun texts() = posted.flatten().flatMap { notice -> notice.messages.map { it.text } }

    @Test
    fun newMessagesOfOthersAreShownOnceAndHistoryNever() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        server.post(room, sender = bob, text = "old")
        sync.syncNow()
        val notifier = notifier()
        // The history of the first load counts as read.
        notifier.check()
        assertEquals(emptyList<String>(), texts())

        server.post(room, sender = bob, text = "new")
        server.post(room, sender = me, text = "mine")
        sync.syncNow()
        notifier.check()
        notifier.check()
        assertEquals(listOf("new 2"), texts())
        assertEquals("bob", posted.single().single().messages.single().peerName)
    }

    @Test
    fun onScreenNothingIsShownAndNothingPopsUpLater() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        sync.syncNow()
        val notifier = notifier()
        foreground.value = true
        server.post(room, sender = bob, text = "seen")
        sync.syncNow()
        notifier.check()
        foreground.value = false
        notifier.check()
        assertEquals(emptyList<String>(), texts())
    }

    @Test
    fun aReadRoomIsNotShown() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        sync.syncNow()
        server.post(room, sender = bob, text = "read")
        sync.syncNow()
        sync.markRead(room)
        notifier().check()
        assertEquals(emptyList<String>(), texts())
    }
}
