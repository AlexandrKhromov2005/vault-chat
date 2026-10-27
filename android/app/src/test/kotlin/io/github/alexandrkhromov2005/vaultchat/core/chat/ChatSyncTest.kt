package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import kotlinx.coroutines.flow.first
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.concurrent.atomic.AtomicBoolean

@RunWith(RobolectricTestRunner::class)
class ChatSyncTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val peer = "00000000-0000-4000-8000-0000000000bb"
    private val server = FakeChatServer(me)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        dao = database.dao()
    }

    @After
    fun tearDown() {
        scope.cancel()
        database.close()
    }

    private fun sync(limits: ChatSync.Limits = ChatSync.Limits()) =
        ChatSync(dao, me, server.requests, scope, System::currentTimeMillis, limits)

    private suspend fun eventually(condition: suspend () -> Boolean) {
        withTimeout(5_000) { while (!condition()) delay(10) }
    }

    private fun String.isCatchUp(roomId: String) = Regex("messages $roomId cursor=null after=[0-9]+").matches(this)

    private suspend fun stored(roomId: String) = dao.storedMessages(roomId).map { it.sequence }

    @Test
    fun syncListsRoomsInServerOrderAndPreloadsThem() = runBlocking {
        val dialog = server.addDialog(1, peer, "bob")
        val channel = server.addChannel(2)
        server.postMany(dialog, 3, sender = peer)
        val sync = sync()

        assertNull(sync.syncNow())

        assertEquals(listOf(dialog, channel), dao.rooms().map { it.roomId })
        assertEquals(listOf(1L, 2L, 3L), stored(dialog))
        // History present at the first load counts as read.
        assertEquals(3L, dao.timeline(dialog)!!.lastRead)
        assertEquals(0L, dao.timeline(channel)!!.newest)
        eventually { dao.knownProfiles(listOf(peer)).isNotEmpty() }
    }

    @Test
    fun aRoomMissingFromACompleteListingIsDeleted() = runBlocking {
        val kept = server.addChannel(1)
        val gone = server.addChannel(2)
        server.postMany(gone, 2)
        val sync = sync()
        sync.syncNow()

        server.removeRoom(gone)
        assertNull(sync.syncNow())

        assertEquals(listOf(kept), dao.rooms().map { it.roomId })
        assertTrue(stored(gone).isEmpty())
        assertNull(dao.timeline(gone))
    }

    @Test
    fun aTruncatedListingDeletesNothing() = runBlocking {
        val rooms = (1..5).map { server.addChannel(it) }
        sync().syncNow()

        // Two pages of two rooms: the oldest room is beyond them.
        assertNull(sync(ChatSync.Limits(roomPage = 2, roomPages = 2)).syncNow())

        assertNotNull(dao.room(rooms[0]))
        assertEquals(5, dao.rooms().size)
    }

    @Test
    fun aRoomThatMovesToTheTopDuringAListingIsKept() = runBlocking {
        val rooms = (1..4).map { server.addChannel(it) }
        val sync = sync(ChatSync.Limits(roomPage = 2))
        sync.syncNow()
        val oldest = rooms[0]

        // After the first page, the room on the last page gets a message and
        // moves to the top: the second page no longer contains it.
        val once = AtomicBoolean()
        server.afterRoomPage = { if (once.compareAndSet(false, true)) server.post(oldest) }
        assertNull(sync.syncNow())

        assertNotNull(dao.room(oldest))
        assertEquals(oldest, dao.rooms().first().roomId)
    }

    @Test
    fun aRoomAddedByAnEventDuringAListingSurvivesIt() = runBlocking {
        server.addChannel(1)
        val sync = sync()
        val joined = server.id(1, 2)
        val once = AtomicBoolean()
        server.afterRoomPage = {
            if (once.compareAndSet(false, true)) {
                // The page already returned does not list the room; the event
                // arrives while the listing is still under way.
                server.addChannel(2)
                sync.onEvent(Frame.MemberAdded(joined, me))
                // Give the event every chance to be applied before the listing
                // is stored: it must wait for the listing instead.
                runBlocking { withTimeoutOrNull(2_000) { while (dao.room(joined) == null) delay(10) } }
            }
        }
        dao.rooms()

        assertNull(sync.syncNow())

        eventually { dao.room(joined) != null && dao.timeline(joined) != null }
    }

    @Test
    fun aGapInLiveMessagesIsFilledByCatchUp() = runBlocking {
        val room = server.addChannel(1)
        server.postMany(room, 3)
        val sync = sync()
        sync.syncNow()

        server.post(room)
        val fifth = server.post(room)
        sync.onEvent(Frame.MessageCreated(room, fifth))

        eventually { stored(room) == (1L..5L).toList() }
        assertEquals(5L, dao.timeline(room)!!.newest)
    }

    @Test
    fun aLiveMessageExtendsTheRoomAndMovesItUp() = runBlocking {
        val first = server.addChannel(1)
        val second = server.addChannel(2)
        val sync = sync()
        sync.syncNow()
        assertEquals(second, dao.rooms().first().roomId)

        sync.onEvent(Frame.MessageCreated(first, server.post(first, sender = peer)))

        eventually { stored(first) == listOf(1L) }
        assertEquals(first, dao.rooms().first().roomId)
        // It directly followed the stored block: no catch-up was needed.
        assertTrue(server.calls().none { it.isCatchUp(first) })
    }

    @Test
    fun aMessageForAnUnknownRoomListsIt() = runBlocking {
        server.addChannel(1)
        val sync = sync()
        sync.syncNow()
        val other = server.addChannel(2)

        sync.onEvent(Frame.MessageCreated(other, server.post(other, sender = peer)))

        eventually { stored(other) == listOf(1L) }
    }

    @Test
    fun theUserLeavingARoomDeletesIt() = runBlocking {
        val room = server.addChannel(1)
        server.postMany(room, 2)
        val sync = sync()
        sync.syncNow()

        sync.onEvent(Frame.MemberGone(room, peer))
        sync.onEvent(Frame.MemberGone(room, me))

        eventually { dao.room(room) == null }
        assertTrue(stored(room).isEmpty())
    }

    @Test
    fun aRoomAnsweredWith404IsDeleted() = runBlocking {
        val room = server.addChannel(1)
        val sync = sync()
        sync.syncNow()

        server.intercept = { if (it == "messages $room") 404 else null }
        sync.openRoom(room)

        assertNull(dao.room(room))
    }

    @Test
    fun aRateLimitPausesEveryRequestAndRetries() = runBlocking {
        val room = server.addChannel(1)
        val limited = AtomicBoolean(true)
        server.intercept = { if (it == "rooms" && limited.getAndSet(false)) 429 else null }
        val sync = sync()

        val started = System.currentTimeMillis()
        assertEquals(429, sync.syncNow()?.status)
        // Requests within the pause wait for it.
        assertNull(sync.syncNow())
        assertTrue(System.currentTimeMillis() - started >= 1_000)
        assertNotNull(dao.room(room))

        // The sync that failed is also retried by itself.
        eventually { server.calls().count { it == "rooms null" } >= 3 }
    }

    @Test
    fun aCatchUpBeyondItsBudgetResetsTheRoom() = runBlocking {
        val room = server.addChannel(1)
        server.postMany(room, 3)
        val sync = sync(ChatSync.Limits(history = 3, catchUp = 2, catchUpPages = 2))
        sync.syncNow()

        server.postMany(room, 17)
        assertNull(sync.syncNow())

        assertEquals(listOf(18L, 19L, 20L), stored(room))
        assertEquals(18L, dao.timeline(room)!!.oldest)
        assertEquals(2, server.calls().count { it.isCatchUp(room) })
    }

    @Test
    fun aRoomCatchesUpAcrossPages() = runBlocking {
        val room = server.addChannel(1)
        server.postMany(room, 3)
        val sync = sync(ChatSync.Limits(history = 3, catchUp = 2))
        sync.syncNow()

        server.postMany(room, 5)
        assertNull(sync.syncNow())

        assertEquals((1L..8L).toList(), stored(room))
    }

    @Test
    fun olderPagesAreLoadedUntilTheStart() = runBlocking {
        val room = server.addChannel(1)
        server.postMany(room, 7)
        val sync = sync(ChatSync.Limits(history = 3))
        sync.syncNow()
        assertEquals(listOf(5L, 6L, 7L), stored(room))

        assertEquals(false, sync.loadOlder(room))
        assertEquals(true, sync.loadOlder(room))

        assertEquals((1L..7L).toList(), stored(room))
        assertEquals(true, sync.loadOlder(room))
    }

    @Test
    fun aMalformedCatchUpResetsTheRoomInsteadOfStoringIt() = runBlocking {
        val room = server.addChannel(1)
        server.postMany(room, 3)
        val sync = sync()
        sync.syncNow()
        server.postMany(room, 2)

        // The server skips message 4.
        server.messages.getValue(room).removeAt(3)
        assertEquals(ApiException.INVALID_RESPONSE, sync.syncNow()?.code)

        // The latest page itself is not consecutive and is rejected too: the
        // stored block stays as it was.
        assertEquals(listOf(1L, 2L, 3L), stored(room))
    }

    private suspend fun unread(roomId: String) = dao.roomList(me).first().single { it.room.roomId == roomId }.unread

    // Review of 4fba109: a room loaded because of a live message counted that
    // very message as read.
    @Test
    fun aLiveMessageInARoomThatWasNotPreloadedIsUnread() = runBlocking {
        val notPreloaded = server.addChannel(1)
        server.postMany(notPreloaded, 2, sender = peer)
        (2..11).forEach { server.addChannel(it) }
        val sync = sync()
        assertNull(sync.syncNow())
        assertNull(dao.timeline(notPreloaded))

        sync.onEvent(Frame.MessageCreated(notPreloaded, server.post(notPreloaded, sender = peer)))
        eventually { dao.storedMessages(notPreloaded).size == 3 }

        // Older history counts as read; the new message does not.
        assertEquals(1, unread(notPreloaded))
    }

    @Test
    fun theFirstMessageOfANewDialogIsUnread() = runBlocking {
        val sync = sync()
        assertNull(sync.syncNow())

        val dialog = server.addDialog(1, peer, "bob")
        val message = server.post(dialog, sender = peer)
        sync.onEvent(Frame.MemberAdded(dialog, me))
        sync.onEvent(Frame.MessageCreated(dialog, message))
        eventually { dao.storedMessages(dialog).isNotEmpty() }

        assertEquals(1, unread(dialog))
    }

    // Review of 4fba109: times were compared in milliseconds, but the server
    // sends microseconds.
    @Test
    fun aMissedMessageInTheSameMillisecondIsCaughtUp() = runBlocking {
        val room = server.addChannel(1)
        fun put(sequence: Long, at: String) = synchronized(server) {
            server.messages.getValue(room) += io.github.alexandrkhromov2005.vaultchat.core.net.MessageDto(
                messageId = server.id(2, sequence.toInt()),
                roomId = room,
                senderId = peer,
                clientMessageId = server.id(3, sequence.toInt()),
                text = "text $sequence",
                sequence = sequence,
                createdAt = at,
            )
            val index = server.rooms.indexOfFirst { it.roomId == room }
            server.rooms[index] = server.rooms[index].copy(lastMessageAt = at)
        }
        put(1, "2026-10-27T12:00:00.1234Z")
        val sync = sync()
        assertNull(sync.syncNow())

        // The live event of the second message was lost.
        put(2, "2026-10-27T12:00:00.123901Z")
        assertNull(sync.syncNow())

        assertEquals(listOf(1L, 2L), stored(room))
    }

    // Review of 4fba109: profiles and catch-ups were sent as fast as two
    // connections allowed, far over the Gateway's burst.
    @Test
    fun backgroundRequestsStayWithinTheBudget() = runBlocking {
        (1..100).forEach { n -> server.addDialog(n, server.id(4, n), "user$n") }
        val sync = sync()
        val started = System.currentTimeMillis()
        assertNull(sync.syncNow())
        delay(2_000)

        val seconds = (System.currentTimeMillis() - started) / 1000.0
        // Half of the Gateway's limit (120/min, burst 30) is left for the user.
        val allowed = 15 + seconds * 60 / 60.0
        assertTrue("${server.calls().size} requests in $seconds s", server.calls().size <= allowed + 1)
        assertTrue(dao.knownProfiles((1..100).map { server.id(4, it) }).isNotEmpty())
    }

    @Test
    fun aRoomOpenedByTheUserDoesNotWaitForBackgroundRequests() = runBlocking {
        (1..100).forEach { n -> server.addDialog(n, server.id(4, n), "user$n") }
        val room = server.addChannel(200)
        val sync = sync(ChatSync.Limits(preloadedRooms = 0))
        sync.syncNow()
        delay(200)

        val started = System.currentTimeMillis()
        sync.openRoom(room)
        assertTrue(System.currentTimeMillis() - started < 1_000)
        assertNotNull(dao.timeline(room))
    }

    // Review of 4fba109: each live message of a room that was loading started
    // a refresh of its own.
    @Test
    fun liveMessagesDuringALoadCoalesce() = runBlocking {
        val room = server.addChannel(1)
        (2..11).forEach { server.addChannel(it) }
        val sync = sync()
        assertNull(sync.syncNow())
        assertNull(dao.timeline(room))

        val slow = AtomicBoolean(true)
        server.intercept = { call ->
            if (call == "messages $room" && slow.getAndSet(false)) Thread.sleep(500)
            null
        }
        repeat(20) { sync.onEvent(Frame.MessageCreated(room, server.post(room, sender = peer))) }
        eventually { dao.timeline(room)?.newest == 20L }
        delay(1_000)

        val reads = server.calls().count { it.startsWith("messages $room") }
        assertTrue("$reads history requests for one room", reads <= 2)
    }

    private suspend fun within(ms: Long, condition: suspend () -> Boolean): Boolean =
        withTimeoutOrNull(ms) { while (!condition()) delay(20); true } ?: false

    private fun dialogs() = (1..100).forEach { n -> server.addDialog(n, server.id(4, n), "user$n") }

    // Second review: profiles filled the background budget, and work that
    // keeps the data correct waited behind them.
    @Test
    fun aGapIsCaughtUpWhileProfilesLoad() = runBlocking {
        dialogs()
        val channel = server.addChannel(200)
        val sync = sync(ChatSync.Limits(preloadedRooms = 0))
        assertNull(sync.syncNow())
        sync.openRoom(channel)

        server.post(channel, sender = peer)
        // The first message's event was lost.
        sync.onEvent(Frame.MessageCreated(channel, server.post(channel, sender = peer)))

        assertTrue("gap filled within 3 s", within(3_000) { stored(channel) == listOf(1L, 2L) })
    }

    @Test
    fun liveEventsAreNotHeldBackByAJoinedRoom() = runBlocking {
        dialogs()
        val channel = server.addChannel(200)
        val sync = sync(ChatSync.Limits(preloadedRooms = 0))
        assertNull(sync.syncNow())
        sync.openRoom(channel)

        sync.onEvent(Frame.MemberAdded(server.addChannel(201), me))
        sync.onEvent(Frame.MessageCreated(channel, server.post(channel, sender = peer)))

        assertTrue("a live message of a loaded room is stored within 2 s", within(2_000) { stored(channel).isNotEmpty() })
    }

    @Test
    fun aPreloadedRoomOpenedDuringTheFirstSyncOpensAtOnce() = runBlocking {
        dialogs()
        val top = server.rooms.first().roomId
        val sync = sync()
        scope.launch { sync.syncNow() }
        delay(300)

        assertNotNull("openRoom waited for the background preload", withTimeoutOrNull(3_000) { sync.openRoom(top) })
    }

    @Test
    fun aRoomLeftWhileItWasBeingReadIsNotStored() = runBlocking {
        val sync = sync()
        assertNull(sync.syncNow())
        val joined = server.addChannel(1)
        server.intercept = { call ->
            // The answer is on its way when the user leaves the room.
            if (call == "room $joined") {
                sync.onEvent(Frame.MemberGone(joined, me))
                Thread.sleep(300)
            }
            null
        }

        sync.onEvent(Frame.MemberAdded(joined, me))
        eventually { server.calls().contains("room $joined") }
        delay(1_000)

        assertNull(dao.room(joined))
    }

    @Test
    fun theHistoryOfAChannelTheUserWasAddedToCountsAsRead() = runBlocking {
        val sync = sync()
        assertNull(sync.syncNow())
        val channel = server.addChannel(1)
        server.postMany(channel, 3, sender = peer)

        sync.onEvent(Frame.MemberAdded(channel, me))
        eventually { stored(channel).size == 3 }

        assertEquals(0, unread(channel))
    }
}
