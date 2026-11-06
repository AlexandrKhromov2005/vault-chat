package io.github.alexandrkhromov2005.vaultchat.core.chat.db

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class ChatDaoTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val peer = "00000000-0000-4000-8000-0000000000bb"
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        dao = database.dao()
    }

    @After
    fun tearDown() {
        database.close()
    }

    private fun room(n: Int, kind: String = RoomEntity.CHANNEL, peerId: String? = null) = RoomEntity(
        roomId = "room-$n",
        kind = kind,
        name = "room $n",
        ownerId = null,
        isPrivate = false,
        createdAt = 0,
        peerId = peerId,
        lastMessageAt = null,
        activity = 0,
    )

    private fun message(roomId: String, sequence: Long, sender: String = peer) = MessageEntity(
        roomId = roomId,
        sequence = sequence,
        messageId = "$roomId-$sequence",
        senderId = sender,
        clientMessageId = "c-$sequence",
        text = "text $sequence",
        attachments = "",
        createdAt = sequence * 1000,
    )

    private fun newestFirst(roomId: String, range: LongProgression) = range.map { message(roomId, it) }

    /** The stored block is exactly oldest..newest. */
    private suspend fun assertContiguous(roomId: String) {
        val block = dao.timeline(roomId)!!
        assertEquals((block.oldest..block.newest).toList(), dao.storedMessages(roomId).map { it.sequence })
    }

    @Test
    fun pagesAndLiveMessagesKeepTheBlockContiguous() = runBlocking {
        dao.storeRoomList(listOf(room(1)), complete = true)
        val id = "room-1"

        dao.applyLatest(id, newestFirst(id, 100L downTo 91L))
        assertContiguous(id)
        assertEquals(ChatDao.Following.APPLIED, dao.applyFollowing(id, listOf(message(id, 101)), live = true))
        assertEquals(ChatDao.Following.GAP, dao.applyFollowing(id, listOf(message(id, 103)), live = true))
        assertContiguous(id)
        dao.applyOlder(id, newestFirst(id, 90L downTo 81L))
        // Dropped: it does not directly precede the block.
        dao.applyOlder(id, newestFirst(id, 70L downTo 61L))
        assertContiguous(id)
        // A distant latest page replaces the block.
        dao.applyLatest(id, newestFirst(id, 300L downTo 291L))
        assertContiguous(id)
        assertEquals(291L, dao.timeline(id)!!.oldest)
    }

    @Test
    fun concurrentWritesKeepTheBlockContiguous() = runBlocking {
        dao.storeRoomList(listOf(room(1)), complete = true)
        val id = "room-1"
        dao.applyLatest(id, newestFirst(id, 50L downTo 41L))

        (51L..150L).map { sequence ->
            async(Dispatchers.IO) { dao.applyFollowing(id, listOf(message(id, sequence)), live = true) }
        }.awaitAll()

        assertContiguous(id)
    }

    @Test
    fun messagesForAnUnknownOrUnloadedRoomAreNotStored() = runBlocking {
        dao.storeRoomList(listOf(room(1)), complete = true)

        assertEquals(ChatDao.Following.UNKNOWN_ROOM, dao.applyFollowing("room-9", listOf(message("room-9", 1)), live = true))
        assertEquals(ChatDao.Following.NOT_LOADED, dao.applyFollowing("room-1", listOf(message("room-1", 1)), live = true))
        assertTrue(dao.storedMessages("room-1").isEmpty())
    }

    @Test
    fun aCompleteListingDeletesUnlistedRoomsWithTheirMessages() = runBlocking {
        dao.storeRoomList(listOf(room(1), room(2)), complete = true)
        dao.applyLatest("room-2", newestFirst("room-2", 3L downTo 1L))

        assertEquals(listOf("room-2"), dao.storeRoomList(listOf(room(1)), complete = true))

        assertNull(dao.room("room-2"))
        assertNull(dao.timeline("room-2"))
        assertTrue(dao.storedMessages("room-2").isEmpty())
        assertEquals(emptyList<String>(), dao.storeRoomList(listOf(room(3)), complete = false))
        assertEquals(listOf("room-3", "room-1"), dao.rooms().map { it.roomId })
    }

    @Test
    fun theListShowsTheLatestMessageUnreadCountAndPeer() = runBlocking {
        dao.storeRoomList(listOf(room(1, RoomEntity.DIRECT, peerId = peer), room(2)), complete = true)
        dao.upsertProfiles(listOf(ProfileEntity(peer, "bob")))
        dao.applyLatest("room-1", newestFirst("room-1", 2L downTo 1L))
        dao.applyLatest("room-2", emptyList())

        dao.applyFollowing("room-1", listOf(message("room-1", 3), message("room-1", 4, sender = me)), live = true)
        dao.applyFollowing("room-2", listOf(message("room-2", 1)), live = true)
        dao.applyFollowing("room-1", listOf(message("room-1", 5)), live = true)

        val list = dao.roomList(me).first()
        assertEquals(listOf("room-1", "room-2"), list.map { it.room.roomId })
        val dialog = list.first()
        assertEquals("bob", dialog.peerName)
        assertEquals("text 5", dialog.lastText)
        // Messages 3 and 5 are from the peer; 4 is the user's own.
        assertEquals(2, dialog.unread)
        assertEquals(1, list[1].unread)

        dao.markRead("room-1", 5)
        assertEquals(0, dao.roomList(me).first().first().unread)
    }

    @Test
    fun unreadMessagesBeyondTheStoredBlockAreFlagged() = runBlocking {
        dao.storeRoomList(listOf(room(1)), complete = true)
        dao.applyLatest("room-1", newestFirst("room-1", 10L downTo 1L))
        assertEquals(false, dao.roomList(me).first().single().unreadMore)

        // The room fell far behind and was reset: messages 11..90 are unread
        // but not stored.
        dao.applyLatest("room-1", newestFirst("room-1", 100L downTo 91L))

        val item = dao.roomList(me).first().single()
        assertEquals(10, item.unread)
        assertEquals(true, item.unreadMore)
    }

    @Test
    fun aRosterGoesWithItsRoomButSurvivesAListing() = runBlocking {
        dao.storeRoomList(listOf(room(1), room(2)), complete = true)
        dao.storeRoster("room-1", listOf(MemberEntity("room-1", me, 1), MemberEntity("room-1", peer, 2)), complete = true)

        // A listing updates the room in place; its roster stays.
        dao.storeRoomList(listOf(room(1)), complete = true)
        assertEquals(listOf(me, peer), dao.memberIds("room-1"))

        // An event adds only to a stored roster.
        dao.addMember("room-2", peer)
        dao.storeRoomList(listOf(room(1), room(2)), complete = true)
        dao.addMember("room-2", peer)
        assertTrue(dao.memberIds("room-2").isEmpty())

        dao.deleteRooms(listOf("room-1"))
        assertTrue(dao.memberIds("room-1").isEmpty())
        assertNull(dao.rosterFlow("room-1").first())
        assertTrue(!dao.storeRoster("room-1", emptyList(), complete = true))
    }
}
