package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.IOException
import java.util.Collections
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
class OutboxTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val peer = "00000000-0000-4000-8000-0000000000bb"
    private val server = FakeChatServer(me)
    // Like ChatSession's scope: a failed task is swallowed.
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default + CoroutineExceptionHandler { _, _ -> })
    private val network = MutableStateFlow<Any?>(Unit)
    private val keys = AtomicInteger()
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

    private fun key(n: Int) = server.id(4, n)

    private fun outbox(scope: CoroutineScope = this.scope) =
        Outbox(dao, me, server.requests, sync, scope, network, newId = { key(keys.incrementAndGet()) }, backoff = { Backoff(20, 100) })

    private suspend fun eventually(condition: suspend () -> Boolean) {
        withTimeout(5_000) { while (!condition()) delay(10) }
    }

    private suspend fun loadedChannel(n: Int): String {
        val roomId = server.addChannel(n)
        assertNull(sync.syncNow())
        return roomId
    }

    private suspend fun stored(roomId: String) = dao.storedMessages(roomId).map { it.text }

    private suspend fun queue(roomId: String) = dao.storedOutgoing(roomId)

    private fun sends() = server.calls().filter { it.startsWith("send ") }

    private suspend fun within(ms: Long, condition: suspend () -> Boolean): Boolean =
        withTimeoutOrNull(ms) { while (!condition()) delay(20); true } ?: false

    private fun sql(statement: String) = database.openHelper.writableDatabase.execSQL(statement)

    // Review of 2440b66. Each of these failed on it.

    /**
     * Stage 3 accepts that a listing during which more than a page of rooms
     * moves deletes some as unlisted, since the next sync brings them and
     * their history back. The queue exists only on this device: a room with
     * unsent messages is deleted only once the server confirms it is gone.
     */
    @Test
    fun aRoomMissedByAListingKeepsItsQueue() = runBlocking {
        // Listed as R6 R5 | R4 R3 | R2 R1 with two rooms per page.
        val rooms = (1..6).map { server.addChannel(it) }
        val target = rooms[0]
        sync = ChatSync(dao, me, server.requests, scope, System::currentTimeMillis, ChatSync.Limits(roomPage = 2))
        assertNull(sync.syncNow())
        network.value = null
        outbox().send(target, "pending")

        // After the second page, the target and then two rooms of the first
        // page get messages: the last page and the first page read again both
        // miss the target, and the listing is complete.
        val armed = AtomicBoolean(true)
        val pages = AtomicInteger()
        server.afterRoomPage = {
            if (armed.get() && pages.incrementAndGet() == 2) {
                armed.set(false)
                server.post(target, sender = peer)
                server.post(rooms[5], sender = peer)
                server.post(rooms[4], sender = peer)
            }
        }
        assertNull(sync.syncNow())
        assertNull(sync.syncNow())
        assertNotNull(dao.room(target))

        assertEquals(listOf("pending"), queue(target).map { it.text })
    }

    /** A database failure while starting rooms must not end sending for the life of the process. */
    @Test
    fun aDatabaseErrorWhileStartingRoomsDoesNotStopSending() = runBlocking {
        val room = loadedChannel(1)
        sql("ALTER TABLE outbox RENAME TO outbox_unavailable")
        val outbox = outbox()
        delay(300)
        sql("ALTER TABLE outbox_unavailable RENAME TO outbox")

        outbox.send(room, "hello")
        outbox.kick()

        assertTrue(within(3_000) { server.sent(room).isNotEmpty() })
    }

    /** Nor may one between two sends leave the room marked as being sent. */
    @Test
    fun aDatabaseErrorBetweenSendsDoesNotStickTheRoom() = runBlocking {
        val room = loadedChannel(1)
        val failing = AtomicBoolean(true)
        val attempts = AtomicInteger()
        server.intercept = { if (it == "send $room" && failing.get()) 503.also { attempts.incrementAndGet() } else null }
        val outbox = outbox()
        outbox.send(room, "hello")
        assertTrue(within(3_000) { attempts.get() >= 1 })

        sql("ALTER TABLE outbox RENAME TO outbox_unavailable")
        delay(500)
        sql("ALTER TABLE outbox_unavailable RENAME TO outbox")
        failing.set(false)

        outbox.send(room, "again")
        outbox.kick()

        assertTrue(within(3_000) { server.sent(room).map { it.text } == listOf("hello", "again") })
    }

    /** A failed message is the room's preview only until a newer message is stored. */
    @Test
    fun aNewerMessageFromAPeerIsThePreviewNotAnOldFailedOne() = runBlocking {
        val room = loadedChannel(1)
        server.intercept = { if (it == "send $room") 400 else null }
        outbox().send(room, "rejected")
        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.FAILED }
        assertEquals("rejected", dao.roomList(me).first().single().pendingText)
        server.intercept = { null }

        sync.onEvent(Frame.MessageCreated(room, server.post(room, sender = peer, text = "newer")))
        eventually { dao.storedMessages(room).isNotEmpty() }

        assertNull(dao.roomList(me).first().single().pendingText)
    }

    /**
     * A failed send is repeated per room; without a common budget, a Chat
     * outage with many queued rooms would exceed the Gateway's limit and its
     * 429 would pause sync too.
     */
    @Test
    fun repeatedFailuresInManyRoomsKeepToTheSendBudget() = runBlocking {
        val rooms = (1..6).map { loadedChannel(it) }
        server.intercept = { if (it.startsWith("send ")) 503 else null }
        val outbox = Outbox(
            dao, me, server.requests, sync, scope, network,
            newId = { key(keys.incrementAndGet()) },
            backoff = { Backoff(10, 20) },
            budget = RequestBudget(perMinute = 60, burst = 3, clock = System::currentTimeMillis),
        )

        rooms.forEach { outbox.send(it, "hello") }
        delay(1_000)

        // Three at once, then one a second.
        assertTrue(sends().size.toString(), sends().size <= 5)
    }

    /**
     * Keys are per sender, but a stored message of mine with the same key and
     * another text (the server's 409) proves only that this text was not
     * stored: it must not resolve the failed row.
     */
    @Test
    fun aConflictingStoredMessageDoesNotResolveTheFailedRow() = runBlocking {
        val room = loadedChannel(1)
        server.store(room, me, key(1), "other text")
        assertNull(sync.syncNow())
        val outbox = outbox()
        outbox.send(room, "mine")
        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.FAILED }

        // Any later write of the room runs the resolution.
        sync.onEvent(Frame.MessageCreated(room, server.post(room, sender = peer)))
        eventually { dao.storedMessages(room).size == 2 }

        assertEquals(listOf("mine"), queue(room).map { it.text })
    }

    /** The composer keeps the text unless it was queued. */
    @Test
    fun aMessageForARoomThatIsGoneIsNotReportedAsQueued() = runBlocking {
        val outbox = outbox()

        assertFalse(outbox.send(server.id(1, 99), "hello"))
    }

    @Test
    fun aMessageIsSentAndShownOnce() = runBlocking {
        val room = loadedChannel(1)
        val outbox = outbox()

        assertTrue(outbox.send(room, "hello"))

        eventually { queue(room).isEmpty() }
        assertEquals(listOf("hello"), stored(room))
        assertEquals(listOf("hello"), server.sent(room).map { it.text })
        assertEquals(key(1), dao.storedMessages(room).single().clientMessageId)
    }

    @Test
    fun aLostAnswerIsRepeatedWithTheSameKeyAndStoredOnce() = runBlocking {
        val room = loadedChannel(1)
        val lost = AtomicInteger()
        server.afterSend = { if (lost.getAndIncrement() == 0) throw IOException("connection reset") }
        val outbox = outbox()

        outbox.send(room, "hello")

        eventually { queue(room).isEmpty() }
        assertEquals(listOf("send $room ${key(1)}", "send $room ${key(1)}"), sends())
        assertEquals(1, server.sent(room).size)
        assertEquals(listOf("hello"), stored(room))
    }

    @Test
    fun anEchoBeforeTheAnswerLeavesOneMessage() = runBlocking {
        val room = loadedChannel(1)
        val resolvedByEcho = MutableStateFlow(false)
        server.afterSend = { message ->
            sync.onEvent(Frame.MessageCreated(room, message))
            eventually { dao.storedMessages(room).isNotEmpty() }
            resolvedByEcho.value = queue(room).isEmpty()
        }
        val outbox = outbox()

        outbox.send(room, "hello")

        eventually { sends().size == 1 && queue(room).isEmpty() }
        delay(100)
        // The echo resolved the row while the request was under way.
        assertTrue(resolvedByEcho.value)
        assertEquals(listOf("hello"), stored(room))
    }

    @Test
    fun anEchoAfterTheAnswerChangesNothing() = runBlocking {
        val room = loadedChannel(1)
        val outbox = outbox()
        outbox.send(room, "hello")
        eventually { queue(room).isEmpty() }

        sync.onEvent(Frame.MessageCreated(room, server.sent(room).single()))
        delay(100)

        assertEquals(listOf("hello"), stored(room))
        assertTrue(queue(room).isEmpty())
    }

    @Test
    fun anAnswerAfterAGapIsShownAsSentUntilCatchUpReachesIt() = runBlocking {
        val room = loadedChannel(1)
        server.postMany(room, 2, sender = peer)
        server.intercept = { if (it == "messages $room") 503 else null }
        val outbox = outbox()

        outbox.send(room, "mine")

        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.SENT }
        assertEquals(3L, queue(room).single().sequence)
        assertTrue(stored(room).isEmpty())

        server.intercept = { null }
        assertNull(sync.syncNow())

        assertEquals(listOf("text 1", "text 2", "mine"), stored(room))
        assertTrue(queue(room).isEmpty())
    }

    @Test
    fun aQueueLeftByAnEndedSessionIsSentByTheNextOne() = runBlocking {
        val room = loadedChannel(1)
        network.value = null
        val first = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        outbox(first).send(room, "hello")
        first.cancel()
        network.value = Unit

        outbox()

        eventually { queue(room).isEmpty() }
        assertEquals(listOf("hello"), server.sent(room).map { it.text })
    }

    @Test
    fun aMessageAlreadyFetchedByCatchUpIsNotSentAgain() = runBlocking {
        val room = loadedChannel(1)
        network.value = null
        val first = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        outbox(first).send(room, "hello")
        first.cancel()
        // The server stored it before the process died; its answer never arrived.
        server.store(room, me, key(1), "hello")

        assertNull(sync.syncNow())
        assertTrue(queue(room).isEmpty())
        network.value = Unit
        outbox()
        delay(200)

        assertTrue(sends().isEmpty())
        assertEquals(listOf("hello"), stored(room))
    }

    @Test
    fun aTemporaryFailureKeepsTheOrderOfARoom() = runBlocking {
        val room = loadedChannel(1)
        val failures = AtomicInteger()
        server.intercept = { if (it == "send $room" && failures.getAndIncrement() < 2) 503 else null }
        val outbox = outbox()

        outbox.send(room, "first")
        outbox.send(room, "second")

        eventually { queue(room).isEmpty() }
        assertEquals(listOf("first", "second"), server.sent(room).map { it.text })
        assertEquals(listOf(1L, 2L), server.sent(room).map { it.sequence })
        assertEquals(listOf("first", "second"), stored(room))
    }

    @Test
    fun oneRoomsFailuresDoNotHoldBackAnother() = runBlocking {
        val broken = loadedChannel(1)
        val working = loadedChannel(2)
        server.intercept = { if (it == "send $broken") 503 else null }
        val outbox = outbox()

        outbox.send(broken, "stuck")
        outbox.send(working, "goes")

        eventually { queue(working).isEmpty() }
        assertEquals(listOf("goes"), stored(working))
        assertEquals(OutgoingEntity.QUEUED, queue(broken).single().state)
    }

    @Test
    fun aRateLimitPausesSendingAndSync() = runBlocking {
        val room = loadedChannel(1)
        val times = Collections.synchronizedList(mutableListOf<Long>())
        val limited = AtomicInteger()
        server.intercept = { call ->
            if (call == "send $room") {
                times += System.currentTimeMillis()
                if (limited.getAndIncrement() == 0) 429 else null
            } else {
                null
            }
        }
        val outbox = outbox()

        outbox.send(room, "hello")

        // The fake answers with Retry-After: 1; sync waits for it too.
        eventually { sync.gate.remaining() > 0 }
        assertTrue(sync.gate.remaining() > 500)
        eventually { queue(room).isEmpty() }
        assertTrue(times[1] - times[0] >= 900)
    }

    @Test
    fun rejectedMessagesFailWithoutRetries() = runBlocking {
        val room = loadedChannel(1)
        val status = MutableStateFlow(400)
        server.intercept = { if (it == "send $room") status.value else null }
        val outbox = outbox()

        outbox.send(room, "bad")
        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_INVALID, queue(room).single().error)

        status.value = 404
        outbox.send(room, "gone")
        eventually { queue(room).count { it.state == OutgoingEntity.FAILED } == 2 }
        assertEquals(OutgoingEntity.ERROR_NO_ACCESS, queue(room).last().error)

        delay(200)
        assertEquals(2, sends().size)
    }

    @Test
    fun retryKeepsTheKeyAndMovesToTheEnd() = runBlocking {
        val room = loadedChannel(1)
        val status = MutableStateFlow<Int?>(404)
        server.intercept = { if (it == "send $room") status.value else null }
        val outbox = outbox()
        outbox.send(room, "first")
        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.FAILED }
        status.value = 503
        outbox.send(room, "second")
        eventually { sends().size >= 2 }

        status.value = null
        outbox.retry(key(1))

        eventually { queue(room).isEmpty() }
        assertEquals(listOf("second", "first"), server.sent(room).map { it.text })
        assertEquals(key(1), server.sent(room).last().clientMessageId)
    }

    @Test
    fun aConflictIsRetriedUnderANewKey() = runBlocking {
        val room = loadedChannel(1)
        // The key is taken by another text: a bug, or a key generator gone wrong.
        server.store(room, me, key(1), "other text")
        assertNull(sync.syncNow())
        val outbox = outbox()

        outbox.send(room, "mine")
        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_CONFLICT, queue(room).single().error)

        outbox.retry(key(1))

        eventually { queue(room).isEmpty() }
        assertEquals(listOf("other text", "mine"), server.sent(room).map { it.text })
        assertEquals(key(2), server.sent(room).last().clientMessageId)
    }

    @Test
    fun anAnswerThatIsNotTheQueuedMessageFailsIt() = runBlocking {
        val room = loadedChannel(1)
        server.reply = { it.copy(text = "something else") }
        val outbox = outbox()

        outbox.send(room, "hello")

        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_INVALID, queue(room).single().error)
        assertTrue(stored(room).isEmpty())
    }

    @Test
    fun anotherMembersMessageWithTheSameKeyDoesNotResolveTheRow() = runBlocking {
        val room = loadedChannel(1)
        network.value = null
        val outbox = outbox()
        outbox.send(room, "mine")
        server.store(room, peer, key(1), "theirs")

        assertNull(sync.syncNow())

        assertEquals(listOf("theirs"), stored(room))
        assertEquals(OutgoingEntity.QUEUED, queue(room).single().state)
        network.value = Unit
        eventually { queue(room).isEmpty() }
        assertEquals(listOf("theirs", "mine"), stored(room))
    }

    @Test
    fun nothingIsSentWithoutANetwork() = runBlocking {
        val room = loadedChannel(1)
        network.value = null
        val outbox = outbox()

        outbox.send(room, "hello")
        delay(200)
        assertTrue(sends().isEmpty())

        network.value = Unit
        eventually { queue(room).isEmpty() }
        assertEquals(listOf("hello"), stored(room))
    }

    @Test
    fun aKickCutsTheBackoffShort() = runBlocking {
        val room = loadedChannel(1)
        val failing = MutableStateFlow(true)
        server.intercept = { if (it == "send $room" && failing.value) 503 else null }
        val outbox = Outbox(dao, me, server.requests, sync, scope, network, newId = { key(1) }, backoff = { Backoff(60_000, 60_000) })
        outbox.send(room, "hello")
        eventually { sends().isNotEmpty() }

        failing.value = false
        outbox.kick()

        withTimeout(2_000) { outbox.queuedCount.first { it == 0 } }
        assertEquals(listOf("hello"), stored(room))
    }

    @Test
    fun queuingAndRetryingScheduleTheBackgroundJob() = runBlocking {
        val room = loadedChannel(1)
        server.intercept = { if (it == "send $room") 400 else null }
        val scheduled = AtomicInteger()
        val outbox = Outbox(dao, me, server.requests, sync, scope, network, newId = { key(1) }, onQueued = { scheduled.incrementAndGet() })

        outbox.send(room, "hello")
        assertEquals(1, scheduled.get())
        eventually { queue(room).singleOrNull()?.state == OutgoingEntity.FAILED }
        outbox.retry(key(1))

        assertEquals(2, scheduled.get())
    }

    @Test
    fun invalidTextIsNotQueued() = runBlocking {
        val room = loadedChannel(1)
        val outbox = outbox()

        assertFalse(outbox.send(room, "  "))

        assertTrue(queue(room).isEmpty())
    }

    @Test
    fun onlyFailedMessagesCanBeDeleted() = runBlocking {
        val room = loadedChannel(1)
        network.value = null
        val outbox = outbox()
        outbox.send(room, "queued")

        outbox.delete(key(1))

        assertEquals(1, queue(room).size)
    }

    @Test
    fun queuingMovesTheRoomUpAndPreviewsTheMessage() = runBlocking {
        val first = loadedChannel(1)
        val second = loadedChannel(2)
        network.value = null
        assertEquals(second, dao.roomList(me).first().first().room.roomId)

        outbox().send(first, "pending")

        val top = dao.roomList(me).first().first()
        assertEquals(first, top.room.roomId)
        assertEquals("pending", top.pendingText)
        assertEquals(OutgoingEntity.QUEUED, top.pendingState)
        assertEquals(1, outbox().unsentCount.first())
    }

    @Test
    fun aRoomListingKeepsTheQueue() = runBlocking {
        val room = loadedChannel(1)
        network.value = null
        outbox().send(room, "pending")

        // Rooms are upserted: a replace would delete the queue through its foreign key.
        server.post(room, sender = peer)
        assertNull(sync.syncNow())

        assertEquals(listOf("pending"), queue(room).map { it.text })
    }

    @Test
    fun aDeletedRoomTakesItsQueue() = runBlocking {
        val room = loadedChannel(1)
        network.value = null
        outbox().send(room, "pending")

        server.removeRoom(room)
        assertNull(sync.syncNow())

        assertTrue(queue(room).isEmpty())
    }
}
