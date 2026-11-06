package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import kotlin.random.Random

/**
 * Opening a room takes no room lock, so it runs alongside background
 * catch-up, live events and older pages of the same room. The stored block
 * must stay contiguous and reach the newest message whatever finishes first,
 * including resets to the latest page.
 */
@RunWith(RobolectricTestRunner::class)
class ChatSyncStressTest {
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

    private suspend fun assertContiguous(room: String) {
        val block = dao.timeline(room)!!
        val stored = dao.storedMessages(room).map { it.sequence }
        assertEquals("block $block", (block.oldest..block.newest).toList(), stored)
        assertTrue(block.lastRead <= block.newest)
    }

    @Test
    fun concurrentOpenCatchUpLiveAndOlderKeepTheBlockContiguous() = runBlocking {
        val room = server.addChannel(1)
        // Small pages and a short catch-up budget, so that resets happen too.
        val limits = ChatSync.Limits(history = 7, catchUp = 5, catchUpPages = 3, requestsPerMinute = 600_000, burst = 1_000)
        val sync = ChatSync(dao, me, server.requests, scope, System::currentTimeMillis, limits)
        server.postMany(room, 3, sender = peer)
        sync.syncNow()
        val random = Random(42)
        repeat(60) { round ->
            val count = random.nextInt(1, 40)
            val posted = (1..count).map { server.post(room, sender = peer) }
            val work = listOf(
                scope.async { sync.openRoom(room) },
                scope.async { sync.openRoom(room) },
                scope.async { sync.syncNow() },
                scope.async { posted.shuffled(random).take(5).forEach { sync.onEvent(Frame.MessageCreated(room, it)) } },
                scope.async { repeat(3) { sync.loadOlder(room) } },
            )
            work.awaitAll()
            val newest = server.messages.getValue(room).size.toLong()
            val reached = withTimeoutOrNull(5_000) {
                while (dao.timeline(room)?.newest != newest) delay(10)
                true
            }
            assertEquals("round $round reached the newest message", true, reached)
            // Let scheduled refreshes settle before checking the block.
            delay(50)
            assertContiguous(room)
        }
    }
}
