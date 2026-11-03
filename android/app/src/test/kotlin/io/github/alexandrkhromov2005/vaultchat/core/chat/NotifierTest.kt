package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
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
    private val shown = Collections.synchronizedSet(mutableSetOf<String>())
    private val board = object : NoticeBoard {
        override fun post(account: String, notices: List<RoomNotice>) {
            posted += notices
            shown += notices.map { it.roomId }
        }

        override fun shownRooms(): Set<String> = shown.toSet()

        override fun cancel(roomId: String) {
            shown -= roomId
        }
    }
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
    private fun notifier() = Notifier(dao, me, foreground, board, "account", CoroutineScope(Dispatchers.Unconfined + SupervisorJob()).also { it.cancel() })

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
    fun aNoticeKeepsTheUnreadMessagesItReplaces() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        sync.syncNow()
        val notifier = notifier()
        server.post(room, sender = bob, text = "first")
        sync.syncNow()
        notifier.check()

        server.post(room, sender = bob, text = "second")
        sync.syncNow()
        notifier.check()

        // The same tag replaces the notification: the new one is all the shade holds.
        assertEquals(listOf("first 1", "second 2"), posted.last().single().messages.map { it.text })
        // Nothing new, nothing posted.
        notifier.check()
        assertEquals(2, posted.size)
    }

    @Test
    fun aNoticeHoldsTheLastFiveUnreadOfARoomAndNothingRead() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        sync.syncNow()
        val notifier = notifier()
        server.post(room, sender = bob, text = "read")
        sync.syncNow()
        sync.markRead(room)
        notifier.check()
        assertEquals(emptyList<String>(), texts())

        repeat(7) { server.post(room, sender = bob, text = "m") }
        server.post(room, sender = me, text = "mine")
        sync.syncNow()
        notifier.check()

        assertEquals(listOf("m 4", "m 5", "m 6", "m 7", "m 8"), posted.single().single().messages.map { it.text })
    }

    @Test
    fun aRoomWithManyNewMessagesAlertsOnce() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        sync.syncNow()
        val notifier = notifier()
        repeat(450) { server.post(room, sender = bob, text = "m") }
        sync.syncNow()

        repeat(3) { notifier.check() }

        assertEquals(1, posted.size)
        assertEquals("m 450", posted.single().single().messages.last().text)
    }

    @Test
    fun aMarkThatFailsAlertsNoMoreThanOnce() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        sync.syncNow()
        server.post(room, sender = bob, text = "new")
        sync.syncNow()
        val notifier = notifier()
        val writable = database.openHelper.writableDatabase
        writable.execSQL("CREATE TRIGGER no_mark BEFORE INSERT ON notified BEGIN SELECT RAISE(ABORT, 'disk full'); END")

        repeat(3) { runCatching { notifier.check() } }
        assertTrue("alerted ${posted.size} times while the mark kept failing", posted.size <= 1)

        // Once marks work again the message is shown, and only then for good.
        writable.execSQL("DROP TRIGGER no_mark")
        repeat(2) { notifier.check() }
        assertEquals(listOf("new 1"), texts())
    }

    @Test
    fun aFirstTimeSenderIsShownByName() = runBlocking {
        val room = server.addChannel(1)
        server.profiles[bob] = "bob"
        sync.syncNow()
        val profiles = CompletableDeferred<Unit>()
        server.beforeUser = { profiles.await() }
        val live = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        Notifier(dao, me, foreground, board, "account", live)

        server.post(room, sender = bob, text = "hi")
        sync.syncNow()
        // The change of the database has run through the collector by now.
        delay(500)
        assertEquals("posted before the sender's name was read", emptyList<String>(), texts())

        profiles.complete(Unit)
        withTimeout(5_000) { while (posted.isEmpty()) delay(10) }
        assertEquals("bob", posted.single().single().messages.single().senderName)
        live.cancel()
    }

    @Test
    fun aNameThatCannotBeReadDoesNotHoldBackTheFinalCheck() = runBlocking {
        val room = server.addChannel(1)
        sync.syncNow()
        server.intercept = { if (it.startsWith("user")) 503 else null }

        server.post(room, sender = bob, text = "hi")
        sync.syncNow()
        notifier().check()

        assertEquals(listOf("hi 1"), texts())
        assertEquals(null, posted.single().single().messages.single().senderName)
    }

    @Test
    fun aRoomTheUserLeftLosesItsNotification() = runBlocking {
        val room = server.addDialog(1, bob, "bob")
        val other = server.addChannel(2)
        sync.syncNow()
        server.post(room, sender = bob, text = "new")
        server.post(other, sender = bob, text = "other")
        sync.syncNow()
        val notifier = notifier()
        notifier.check()
        assertEquals(setOf(room, other), shown.toSet())

        sync.forget(room)
        notifier.check()

        assertEquals(setOf(other), shown.toSet())
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
