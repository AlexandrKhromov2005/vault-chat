package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.MessageDto
import io.github.alexandrkhromov2005.vaultchat.core.net.MessagePage
import io.github.alexandrkhromov2005.vaultchat.core.net.RoomDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class PagesTest {
    private val room = "00000000-0000-4000-8000-000000000001"
    private val other = "00000000-0000-4000-8000-000000000002"
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val peer = "00000000-0000-4000-8000-0000000000bb"

    private fun dto(sequence: Long, roomId: String = room, text: String = "hi", attachments: List<String>? = null) = MessageDto(
        messageId = "00000000-0000-4000-8000-%012d".format(sequence),
        roomId = roomId,
        senderId = peer,
        clientMessageId = "00000000-0000-4000-9000-%012d".format(sequence),
        text = text,
        attachmentIds = attachments,
        sequence = sequence,
        createdAt = "2026-10-27T12:00:00.123456789Z",
    )

    private fun page(vararg sequences: Long, cursor: String = "") = MessagePage(sequences.map { dto(it) }, cursor)

    private fun rejected(block: () -> Unit) {
        try {
            block()
            fail("accepted")
        } catch (e: ApiException) {
            assertEquals(ApiException.INVALID_RESPONSE, e.code)
        }
    }

    @Test
    fun aConsecutiveHistoryPageIsAccepted() {
        val messages = historyPage(room, cursor = 11, limit = 50, page(10, 9, 8))

        assertEquals(listOf(10L, 9L, 8L), messages.map { it.sequence })
        assertEquals(1793102400123L, messages.first().createdAt)
    }

    @Test
    fun historyWithAHoleOrAtTheCursorIsRejected() {
        rejected { historyPage(room, cursor = null, limit = 50, page(10, 8)) }
        rejected { historyPage(room, cursor = 10, limit = 50, page(10, 9)) }
        rejected { historyPage(room, cursor = null, limit = 2, page(10, 9, 8)) }
    }

    @Test
    fun aMessageOfAnotherRoomIsRejected() {
        rejected { historyPage(room, cursor = null, limit = 50, MessagePage(listOf(dto(1, roomId = other)), "")) }
    }

    @Test
    fun malformedMessagesAreRejected() {
        rejected { dto(1).copy(senderId = "../users").toEntity(room) }
        rejected { dto(0).toEntity(room) }
        rejected { dto(1).copy(createdAt = "yesterday").toEntity(room) }
        rejected { dto(1, text = "x".repeat(MAX_TEXT_CODE_POINTS + 1)).toEntity(room) }
        rejected { dto(1, attachments = List(MAX_ATTACHMENTS + 1) { "00000000-0000-4000-8000-%012d".format(it) }).toEntity(room) }
    }

    @Test
    fun textIsCountedInCodePoints() {
        // An emoji is two UTF-16 units but one character for the server.
        val text = "😀".repeat(MAX_TEXT_CODE_POINTS)
        assertEquals(text, dto(1, text = text).toEntity(room).text)
    }

    @Test
    fun catchUpMustStartRightAfterThePosition() {
        val (messages, more) = catchUpPage(room, after = 7, limit = 100, page(8, 9, cursor = "9"))
        assertEquals(listOf(8L, 9L), messages.map { it.sequence })
        assertTrue(more)

        rejected { catchUpPage(room, after = 7, limit = 100, page(9, 10)) }
        rejected { catchUpPage(room, after = 7, limit = 100, page(8, 10)) }
    }

    @Test
    fun anEmptyCatchUpPageThatPromisesMoreIsRejected() {
        val (messages, more) = catchUpPage(room, after = 7, limit = 100, page())
        assertTrue(messages.isEmpty())
        assertFalse(more)

        rejected { catchUpPage(room, after = 7, limit = 100, page(cursor = "7")) }
    }

    @Test
    fun aDialogKnowsItsPeer() {
        val entity = RoomDto(room, "direct", createdAt = "2026-10-27T12:00:00Z", participantIds = listOf(me, peer)).toEntity(me, 5)

        assertEquals(peer, entity.peerId)
        assertNull(entity.lastMessageAt)
        assertEquals(5L, entity.activity)
    }

    @Test
    fun aRoomWithAMalformedIdIsRejected() {
        rejected { RoomDto("1 OR 1", "channel", createdAt = "2026-10-27T12:00:00Z").toEntity(me, 0) }
    }
}
