package io.github.alexandrkhromov2005.vaultchat.core.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class NamesTest {
    @Test
    fun queriesFollowTheGatewayPattern() {
        assertNull(UserQuery.check("bo"))
        assertNull(UserQuery.check("Bob_the-2nd"))
        assertNull(UserQuery.check("b" + "o".repeat(31)))
        assertEquals(UserQuery.Problem.TOO_SHORT, UserQuery.check(""))
        assertEquals(UserQuery.Problem.TOO_SHORT, UserQuery.check("b"))
        for (query in listOf("1bob", "_bob", "bo b", "боб", "bob%", "b" + "o".repeat(32))) {
            assertEquals(query, UserQuery.Problem.INVALID, UserQuery.check(query))
        }
        assertEquals("bob", UserQuery.normalize("  bob "))
    }

    @Test
    fun channelNamesFollowTheChatRules() {
        assertNull(ChannelName.check("Новости курса"))
        assertNull(ChannelName.check("я".repeat(80)))
        // Code points, not UTF-16 units: 80 emoji are 160 chars.
        assertNull(ChannelName.check("😀".repeat(80)))
        assertEquals(ChannelName.Problem.TOO_LONG, ChannelName.check("я".repeat(81)))
        assertEquals(ChannelName.Problem.EMPTY, ChannelName.check(""))
        assertEquals(ChannelName.Problem.INVALID_CHARACTER, ChannelName.check("a\u0007b"))
        assertEquals(ChannelName.Problem.INVALID_CHARACTER, ChannelName.check("a\nb"))
        assertEquals(ChannelName.Problem.INVALID_CHARACTER, ChannelName.check("a\u0085b"))
        assertEquals(ChannelName.Problem.INVALID_CHARACTER, ChannelName.check("a\uD800b"))
    }

    @Test
    fun namesAreTrimmedLikeGoTrimSpace() {
        assertEquals("news", ChannelName.normalize("  news　\t"))
        assertEquals("", ChannelName.normalize("   "))
        // Zero-width space is not white space in Go.
        assertEquals("​news", ChannelName.normalize("​news"))
    }
}
