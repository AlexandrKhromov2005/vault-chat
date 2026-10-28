package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.MessageText.Problem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class MessageTextTest {
    // An emoji outside the Basic Multilingual Plane: two UTF-16 units, four UTF-8 bytes.
    private val emoji = "😀"

    @Test
    fun theLimitCountsCodePointsNotUtf16Units() {
        assertNull(MessageText.check(emoji.repeat(4096)))
        assertEquals(Problem.TOO_LONG, MessageText.check(emoji.repeat(4097)))
        assertNull(MessageText.check("a".repeat(4096)))
        assertEquals(Problem.TOO_LONG, MessageText.check("a".repeat(4097)))
    }

    @Test
    fun newlineAndTabAreAllowedOtherControlsAreNot() {
        assertNull(MessageText.check("one\ntwo\tthree"))
        assertEquals(Problem.INVALID_CHARACTER, MessageText.check("a\u0000b"))
        assertEquals(Problem.INVALID_CHARACTER, MessageText.check("a\rb"))
        assertEquals(Problem.INVALID_CHARACTER, MessageText.check("a\u007fb"))
        assertEquals(Problem.INVALID_CHARACTER, MessageText.check("a\u0085b"))
    }

    @Test
    fun anUnpairedSurrogateIsNotValidUtf8() {
        assertEquals(Problem.INVALID_CHARACTER, MessageText.check("a\uD83D"))
        assertEquals(Problem.INVALID_CHARACTER, MessageText.check("\uDE00a"))
    }

    @Test
    fun whitespaceAloneIsEmptyButTextIsNotTrimmed() {
        assertEquals(Problem.EMPTY, MessageText.check(""))
        assertEquals(Problem.EMPTY, MessageText.check(" \n\t"))
        // No-break and ideographic spaces are spaces for the server too.
        assertEquals(Problem.EMPTY, MessageText.check(" 　 "))
        // A zero-width space is not: the server accepts it as content.
        assertNull(MessageText.check("​"))
        assertNull(MessageText.check("  hi  "))
    }

    @Test
    fun lengthIsInCodePoints() {
        assertEquals(2, MessageText.length("a$emoji"))
    }
}
