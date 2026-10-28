package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.MemberDto
import io.github.alexandrkhromov2005.vaultchat.core.net.MemberPage
import io.github.alexandrkhromov2005.vaultchat.core.net.PublicProfile
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.IOException
import java.util.concurrent.atomic.AtomicBoolean

@RunWith(RobolectricTestRunner::class)
class MembershipTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val bob = "00000000-0000-4000-8000-0000000000bb"
    private val carol = "00000000-0000-4000-8000-0000000000cc"
    private val server = FakeChatServer(me)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao
    private lateinit var sync: ChatSync

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        dao = database.dao()
        sync = ChatSync(dao, me, server.requests, scope, System::currentTimeMillis)
        server.profiles[bob] = "bob"
        server.profiles[carol] = "carol"
    }

    @After
    fun tearDown() {
        scope.cancel()
        database.close()
    }

    private fun membership(limits: Membership.Limits = Membership.Limits()) = Membership(dao, me, sync, limits)

    private suspend fun eventually(condition: suspend () -> Boolean) {
        withTimeout(5_000) { while (!condition()) delay(10) }
    }

    private suspend fun failure(block: suspend () -> Unit): ApiException {
        try {
            block()
        } catch (e: ApiException) {
            return e
        }
        fail("expected an ApiException")
        throw AssertionError()
    }

    private suspend fun memberIds(roomId: String) = dao.members(roomId).first().map { it.userId }

    @Test
    fun searchStoresNamesAndDropsTheCaller() = runBlocking {
        server.profiles[me] = "bobby"
        server.searchReply = { page -> page.copy(users = page.users + PublicProfile(me, "bobby")) }

        val users = membership().search("bo")

        assertEquals(listOf(bob), users.map { it.userId })
        assertEquals(listOf(bob), dao.knownProfiles(listOf(bob, me)))
    }

    @Test
    fun aMalformedSearchPageIsRejected() = runBlocking {
        val membership = membership(Membership.Limits(searchPage = 2))
        server.searchReply = { page -> page.copy(users = page.users + PublicProfile("not-a-uuid", "bogus")) }
        assertEquals(ApiException.INVALID_RESPONSE, failure { membership.search("bo") }.code)

        server.searchReply = { page -> page.copy(users = List(3) { PublicProfile(bob, "bob") }) }
        assertEquals(ApiException.INVALID_RESPONSE, failure { membership.search("bo") }.code)
        assertTrue(dao.knownProfiles(listOf(bob)).isEmpty())
    }

    @Test
    fun aQueryTheServerRejectsIsNeverSent() = runBlocking {
        for (query in listOf("b", "1bob", "bo b", "боб")) {
            try {
                membership().search(query)
                fail("searched for $query")
            } catch (e: IllegalArgumentException) {
                // Expected.
            }
        }
        assertTrue(server.calls().none { it.startsWith("search") })
    }

    @Test
    fun openingADialogStoresItAtTheTop() = runBlocking {
        server.addChannel(1)
        sync.syncNow()

        val roomId = membership().openDialog(bob)

        val room = dao.rooms().first()
        assertEquals(roomId, room.roomId)
        assertEquals(bob, room.peerId)
        eventually { dao.knownProfiles(listOf(bob)).isNotEmpty() }
    }

    @Test
    fun anExistingDialogKeepsItsMessages() = runBlocking {
        val dialog = server.addDialog(1, bob, "bob")
        server.postMany(dialog, 3, sender = bob)
        server.addChannel(2)
        sync.syncNow()
        assertEquals(3, dao.storedMessages(dialog).size)
        val listed = dao.room(dialog)!!.lastMessageAt
        assertNotNull(listed)

        // The answer has no last_message_at, like the Gateway's Room.
        server.roomReply = { it.copy(lastMessageAt = null) }
        assertEquals(dialog, membership().openDialog(bob))

        assertEquals(dialog, dao.rooms().first().roomId)
        assertEquals(listed, dao.room(dialog)!!.lastMessageAt)
        assertEquals(3, dao.storedMessages(dialog).size)
        assertEquals(3L, dao.timeline(dialog)!!.newest)
    }

    @Test
    fun aDialogWithOtherParticipantsIsRejected() = runBlocking {
        server.roomReply = { room -> room.copy(participantIds = listOf(me, carol).sorted()) }

        assertEquals(ApiException.INVALID_RESPONSE, failure { membership().openDialog(bob) }.code)
        assertTrue(dao.rooms().isEmpty())

        server.roomReply = { room -> room.copy(kind = "channel") }
        assertEquals(ApiException.INVALID_RESPONSE, failure { membership().openDialog(bob) }.code)
        assertTrue(dao.rooms().isEmpty())
    }

    @Test
    fun aDialogOpenedDuringAListingSurvivesIt() = runBlocking {
        server.addChannel(1)
        val membership = membership()
        val opened = AtomicBoolean()
        var dialog: String? = null
        // The dialog is created after the listing read its only page and
        // before the listing is stored, which deletes unlisted rooms.
        server.afterRoomPage = { index ->
            if (index == 0) {
                scope.launch { dialog = membership.openDialog(bob) }
                val start = System.currentTimeMillis()
                while (server.calls().none { it.startsWith("direct") } && System.currentTimeMillis() - start < 5_000) Thread.sleep(5)
                // Time for the dialog to be stored, if nothing held it back.
                Thread.sleep(200)
                opened.set(true)
            }
        }

        assertNull(sync.syncNow())

        assertTrue(opened.get())
        eventually { dialog != null }
        assertNotNull(dao.room(dialog!!))
    }

    @Test
    fun aCreatedChannelIsStored() = runBlocking {
        val roomId = membership().createChannel("Новости", private = true)

        val room = dao.room(roomId)!!
        assertEquals("Новости", room.name)
        assertEquals(me, room.ownerId)
        assertTrue(room.isPrivate)
        assertEquals(1, server.created())
    }

    @Test
    fun aChannelThatIsNotTheOneAskedForIsRejected() = runBlocking {
        server.roomReply = { room -> room.copy(ownerId = bob) }
        val error = failure { membership().createChannel("news", private = false) }
        assertEquals(ApiException.INVALID_RESPONSE, error.code)
        assertTrue(Membership.isUnknownOutcome(error))

        server.roomReply = { room -> room.copy(name = "other") }
        assertEquals(ApiException.INVALID_RESPONSE, failure { membership().createChannel("news", private = false) }.code)
        // The sync it started lists what the server really created.
        eventually { dao.rooms().size == 2 }
    }

    @Test
    fun aLostAnswerToCreateIsUnknownAndNotRepeated() = runBlocking {
        server.afterCreate = { throw IOException("connection reset") }

        val error = failure { membership().createChannel("news", private = false) }

        assertTrue(Membership.isUnknownOutcome(error))
        assertEquals(1, server.created())
        eventually { dao.rooms().any { it.name == "news" } }
        assertEquals(1, server.created())
    }

    @Test
    fun aRejectedNameIsAKnownOutcome() = runBlocking {
        server.intercept = { if (it == "create") 400 else null }
        assertFalse(Membership.isUnknownOutcome(failure { membership().createChannel("news", private = false) }))
        server.intercept = { if (it == "create") 429 else null }
        assertFalse(Membership.isUnknownOutcome(failure { membership().createChannel("news", private = false) }))
    }

    @Test
    fun aRosterIsReadPageByPageAndReplacesTheStoredOne() = runBlocking {
        val channel = server.addChannel(1)
        val others = (1..5).map { server.id(9, it) }
        others.forEach { server.join(channel, it) }
        sync.syncNow()
        val membership = membership(Membership.Limits(memberPage = 2))

        membership.loadRoster(channel)
        assertEquals((others + me).sorted(), memberIds(channel))
        assertEquals(true, membership.roster(channel).first()?.complete)

        server.members.getValue(channel).remove(others[0])
        membership.loadRoster(channel)
        assertEquals((others.drop(1) + me).sorted(), memberIds(channel))
    }

    @Test
    fun aRosterOverItsBudgetIsIncomplete() = runBlocking {
        val channel = server.addChannel(1)
        (1..5).forEach { server.join(channel, server.id(9, it)) }
        sync.syncNow()
        val membership = membership(Membership.Limits(memberPage = 2, memberPages = 2))

        membership.loadRoster(channel)

        assertEquals(4, memberIds(channel).size)
        assertEquals(false, membership.roster(channel).first()?.complete)
    }

    @Test
    fun aMalformedRosterStoresNothing() = runBlocking {
        val channel = server.addChannel(1)
        (1..3).forEach { server.join(channel, server.id(9, it)) }
        sync.syncNow()
        val membership = membership(Membership.Limits(memberPage = 2))
        val pages = listOf<(MemberPage) -> MemberPage>(
            // Out of order.
            { page -> page.copy(members = page.members.reversed()) },
            // A cursor that is not the last member.
            { page -> page.copy(nextCursor = page.members.first().userId) },
            // Over its limit.
            { page -> page.copy(members = page.members + MemberDto(server.id(9, 99), "2026-10-27T12:00:00Z")) },
            // Not a UUID.
            { page -> page.copy(members = listOf(MemberDto("me", "2026-10-27T12:00:00Z"))) },
        )
        for (reply in pages) {
            server.memberReply = reply
            assertEquals(ApiException.INVALID_RESPONSE, failure { membership.loadRoster(channel) }.code)
            assertTrue(memberIds(channel).isEmpty())
            assertNull(membership.roster(channel).first())
        }
    }

    @Test
    fun eventsOfOtherUsersFollowAStoredRoster() = runBlocking {
        val channel = server.addChannel(1)
        val unread = server.addChannel(2)
        sync.syncNow()
        membership().loadRoster(channel)

        sync.onEvent(Frame.MemberAdded(channel, bob))
        sync.onEvent(Frame.MemberAdded(unread, bob))
        eventually { bob in memberIds(channel) }
        sync.onEvent(Frame.MemberGone(channel, bob))
        eventually { bob !in memberIds(channel) }

        // A room whose roster was never read gets none from an event.
        assertTrue(memberIds(unread).isEmpty())
        assertNull(dao.rosterFlow(unread).first())
    }

    @Test
    fun aRosterThatChangesWhileReadIsReadAgain() = runBlocking {
        val channel = server.addChannel(1)
        sync.syncNow()
        val joined = AtomicBoolean()
        // Bob joins after the roster was read and before it is stored; the
        // event arrives at once and would be overwritten.
        server.afterMemberPage = {
            if (joined.compareAndSet(false, true)) {
                server.join(channel, bob)
                sync.onEvent(Frame.MemberAdded(channel, bob))
                val start = System.currentTimeMillis()
                while (sync.rosterVersion(channel) == 0L && System.currentTimeMillis() - start < 5_000) Thread.sleep(5)
            }
        }

        membership().loadRoster(channel)

        assertEquals(listOf(me, bob).sorted(), memberIds(channel))
    }

    @Test
    fun aRosterOfALostRoomDeletesIt() = runBlocking {
        val channel = server.addChannel(1)
        sync.syncNow()
        server.removeRoom(channel)

        membership().loadRoster(channel)

        assertNull(dao.room(channel))
    }

    @Test
    fun addingAndRemovingUpdateTheRoster() = runBlocking {
        val channel = server.addChannel(1)
        sync.syncNow()
        val membership = membership()
        membership.loadRoster(channel)

        membership.addMember(channel, bob)
        assertEquals(listOf(me, bob).sorted(), memberIds(channel))
        assertTrue(bob in server.members.getValue(channel))

        membership.removeMember(channel, bob)
        assertEquals(listOf(me), memberIds(channel))
        assertFalse(bob in server.members.getValue(channel))
    }

    @Test
    fun addingAnUnknownUserKeepsTheRoom() = runBlocking {
        val channel = server.addChannel(1)
        sync.syncNow()

        val error = failure { membership().addMember(channel, server.id(9, 1)) }

        assertEquals(404, error.status)
        assertNotNull(dao.room(channel))
        assertTrue(server.calls().contains("room $channel"))
    }

    @Test
    fun addingToALostRoomDeletesIt() = runBlocking {
        val channel = server.addChannel(1)
        sync.syncNow()
        server.removeRoom(channel)

        assertEquals(404, failure { membership().addMember(channel, bob) }.status)

        assertNull(dao.room(channel))
    }

    @Test
    fun removingFromALostRoomDeletesIt() = runBlocking {
        val channel = server.addChannel(1)
        sync.syncNow()
        server.removeRoom(channel)

        assertEquals(404, failure { membership().removeMember(channel, bob) }.status)

        assertNull(dao.room(channel))
    }

    @Test
    fun leavingDeletesTheRoomWithItsQueue() = runBlocking {
        val channel = server.addForeignChannel(1, owner = bob)
        sync.syncNow()
        val outbox = Outbox(dao, me, server.requests, sync, scope, kotlinx.coroutines.flow.MutableStateFlow(null))
        assertTrue(outbox.send(channel, "unsent"))

        membership().leave(channel)

        assertNull(dao.room(channel))
        assertTrue(dao.storedOutgoing(channel).isEmpty())
        assertTrue(server.rooms.none { it.roomId == channel })
    }

    @Test
    fun leavingAgainAfterALostAnswerIsTheSameOutcome() = runBlocking {
        val channel = server.addForeignChannel(1, owner = bob)
        sync.syncNow()
        // The first leave reached the server, but its answer was lost.
        server.removeRoom(channel)

        membership().leave(channel)

        assertNull(dao.room(channel))
    }

    @Test
    fun theOwnerCannotLeave() = runBlocking {
        val channel = server.addChannel(1)
        sync.syncNow()

        assertEquals(409, failure { membership().leave(channel) }.status)

        assertNotNull(dao.room(channel))
    }

    @Test
    fun aRoomReadForAnEarlierEventIsNotStoredAfterLeaving() = runBlocking {
        val channel = server.addForeignChannel(1, owner = bob)
        sync.syncNow()
        dao.deleteRooms(listOf(channel))
        val membership = membership()
        val room = server.rooms.single { it.roomId == channel }
        // member.added makes sync read the room. The user leaves after the
        // server answered that read, so the answer still has the room.
        val left = AtomicBoolean()
        server.intercept = { call ->
            if (call == "room $channel" && left.compareAndSet(false, true)) {
                runBlocking { membership.leave(channel) }
                server.rooms.add(room)
            }
            null
        }

        sync.onEvent(Frame.MemberAdded(channel, me))
        eventually { left.get() }
        delay(200)

        assertNull(dao.room(channel))
    }
}
