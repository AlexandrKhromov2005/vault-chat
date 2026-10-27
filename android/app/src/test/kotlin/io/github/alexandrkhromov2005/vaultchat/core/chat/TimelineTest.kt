package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.TimelineEntity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TimelineTest {
    private val room = "00000000-0000-4000-8000-000000000001"

    private fun message(sequence: Long) = MessageEntity(
        roomId = room,
        sequence = sequence,
        messageId = "m$sequence",
        senderId = "s",
        clientMessageId = "c$sequence",
        text = "text $sequence",
        attachments = "",
        createdAt = sequence * 1000,
    )

    private fun newestFirst(range: LongProgression) = range.map(::message)

    private fun block(oldest: Long, newest: Long, lastRead: Long = newest) = TimelineEntity(room, newest, oldest, lastRead)

    @Test
    fun aFirstLatestPageBecomesTheBlockAndCountsAsRead() {
        val update = Timeline.latest(room, null, newestFirst(100L downTo 51L))!!

        assertEquals(block(oldest = 51, newest = 100, lastRead = 100), update.timeline)
        assertEquals(50, update.insert.size)
        assertTrue(update.replace)
    }

    @Test
    fun anEmptyRoomIsLoadedWithNothingOlder() {
        val update = Timeline.latest(room, null, emptyList())!!

        assertEquals(block(oldest = 1, newest = 0, lastRead = 0), update.timeline)
        assertFalse(update.timeline.hasOlder)
    }

    @Test
    fun aLatestPageThatReachesTheBlockExtendsIt() {
        val update = Timeline.latest(room, block(oldest = 51, newest = 100, lastRead = 90), newestFirst(120L downTo 71L))!!

        assertEquals(block(oldest = 51, newest = 120, lastRead = 90), update.timeline)
        assertEquals((101L..120L).toList(), update.insert.map { it.sequence }.sorted())
        assertFalse(update.replace)
    }

    @Test
    fun aLatestPageBeyondAGapReplacesTheBlock() {
        // Messages 101..149 are unknown; keeping 51..100 would break the block.
        val update = Timeline.latest(room, block(oldest = 51, newest = 100), newestFirst(199L downTo 150L))!!

        assertEquals(block(oldest = 150, newest = 199, lastRead = 100), update.timeline)
        assertTrue(update.replace)
    }

    @Test
    fun aLatestPageWithNothingNewChangesNothing() {
        assertNull(Timeline.latest(room, block(oldest = 51, newest = 100), newestFirst(100L downTo 51L)))
        assertNull(Timeline.latest(room, block(oldest = 51, newest = 100), emptyList()))
    }

    @Test
    fun followingMessagesExtendDuplicatesAreSkipped() {
        val extension = Timeline.extend(block(oldest = 1, newest = 10), listOf(message(9), message(10), message(11), message(12)))

        assertFalse(extension.gap)
        assertEquals(12L, extension.update!!.timeline.newest)
        assertEquals(listOf(11L, 12L), extension.update!!.insert.map { it.sequence })
    }

    @Test
    fun aMessageAfterAGapIsNotStored() {
        val extension = Timeline.extend(block(oldest = 1, newest = 10), listOf(message(11), message(13)))

        assertTrue(extension.gap)
        assertEquals(11L, extension.update!!.timeline.newest)
        assertEquals(listOf(11L), extension.update!!.insert.map { it.sequence })
    }

    @Test
    fun anUnloadedRoomCannotBeExtended() {
        val extension = Timeline.extend(null, listOf(message(1)))

        assertTrue(extension.gap)
        assertNull(extension.update)
    }

    @Test
    fun onlyDuplicatesChangeNothing() {
        val extension = Timeline.extend(block(oldest = 1, newest = 10), listOf(message(10)))

        assertFalse(extension.gap)
        assertNull(extension.update)
    }

    @Test
    fun anOlderPageDirectlyBeforeTheBlockIsStored() {
        val update = Timeline.older(block(oldest = 51, newest = 100), newestFirst(50L downTo 1L))!!

        assertEquals(1L, update.timeline.oldest)
        assertFalse(update.timeline.hasOlder)
        assertEquals(50, update.insert.size)
    }

    @Test
    fun anOlderPageThatNoLongerFitsIsDropped() {
        // The block was replaced while the page was loading.
        assertNull(Timeline.older(block(oldest = 151, newest = 200), newestFirst(50L downTo 1L)))
        assertNull(Timeline.older(null, newestFirst(50L downTo 1L)))
    }
}
