package io.github.alexandrkhromov2005.vaultchat.core.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class FrameTest {
    private val room = "00000000-0000-4000-8000-000000000001"
    private val user = "00000000-0000-4000-8000-0000000000aa"

    @Test
    fun readyAndMembershipFramesAreParsed() {
        assertEquals(
            Frame.Ready(user, "2026-10-15T12:15:00Z"),
            Frame.parse("""{"type":"ready","user_id":"$user","expires_at":"2026-10-15T12:15:00Z"}"""),
        )
        assertEquals(Frame.MemberAdded(room, user), Frame.parse("""{"type":"member.added","room_id":"$room","user_id":"$user"}"""))
        assertEquals(Frame.MemberGone(room, user), Frame.parse("""{"type":"member.left","room_id":"$room","user_id":"$user"}"""))
        assertEquals(Frame.MemberGone(room, user), Frame.parse("""{"type":"member.removed","room_id":"$room","user_id":"$user"}"""))
    }

    @Test
    fun aMessageFrameCarriesTheRestMessage() {
        val frame = Frame.parse(
            """{"type":"message.created","room_id":"$room","future":1,"message":{"message_id":"$user","room_id":"$room",""" +
                """"sender_id":"$user","client_message_id":"$user","text":"hi","sequence":7,"created_at":"2026-10-15T12:00:00Z"}}""",
        )

        assertTrue(frame is Frame.MessageCreated)
        assertEquals(7L, (frame as Frame.MessageCreated).message.sequence)
    }

    @Test
    fun unknownIncompleteAndMalformedFramesAreIgnored() {
        assertNull(Frame.parse("""{"type":"room.renamed","room_id":"$room"}"""))
        assertNull(Frame.parse("""{"type":"member.added","room_id":"$room"}"""))
        assertNull(Frame.parse("""{"type":"member.added","room_id":"not-a-room","user_id":"$user"}"""))
        assertNull(Frame.parse("""{"type":"message.created","room_id":"$room","message":{"text":"hi"}}"""))
        assertNull(Frame.parse("""{"type":7}"""))
        assertNull(Frame.parse("""["ready"]"""))
        assertNull(Frame.parse("""{"type":"ready""""))
        assertNull(Frame.parse(""))
    }
}
