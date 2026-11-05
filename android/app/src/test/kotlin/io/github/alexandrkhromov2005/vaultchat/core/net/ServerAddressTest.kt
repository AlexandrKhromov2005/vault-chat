package io.github.alexandrkhromov2005.vaultchat.core.net

import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress.Problem
import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Test

class ServerAddressTest {

    @Test
    fun normalisesToTheRoot() {
        assertEquals("https://chat.example.org", ServerAddress.parse("  chat.example.org/ ", false).toString())
        assertEquals("https://chat.example.org:8443", ServerAddress.parse("HTTPS://Chat.Example.org:8443", false).toString())
        assertEquals("https://192.0.2.10", ServerAddress.parse("192.0.2.10", false).toString())
    }

    @Test
    fun releaseBuildsRejectCleartextEvenToLocalHosts() {
        assertProblem(Problem.CLEARTEXT, "http://10.0.2.2:8080", allowLocalCleartext = false)
        assertProblem(Problem.CLEARTEXT, "http://chat.example.org", allowLocalCleartext = false)
    }

    @Test
    fun debugBuildsAllowCleartextOnlyToLocalHosts() {
        assertEquals("http://10.0.2.2:8080", ServerAddress.parse("http://10.0.2.2:8080", true).toString())
        assertEquals("http://127.0.0.1:8080", ServerAddress.parse("http://127.0.0.1:8080", true).toString())
        assertProblem(Problem.CLEARTEXT, "http://chat.example.org", allowLocalCleartext = true)
        // A local-looking prefix is not a local host.
        assertProblem(Problem.CLEARTEXT, "http://localhost.example.org", allowLocalCleartext = true)
    }

    @Test
    fun rejectsAnythingButAServerRoot() {
        assertProblem(Problem.EMPTY, "   ")
        assertProblem(Problem.MALFORMED, "ftp://chat.example.org")
        assertProblem(Problem.MALFORMED, "https://")
        assertProblem(Problem.MALFORMED, "chat example org")
        assertProblem(Problem.CREDENTIALS, "https://user:secret@chat.example.org")
        assertProblem(Problem.PATH, "https://chat.example.org/api/v1")
        assertProblem(Problem.PATH, "https://chat.example.org/?next=1")
        assertProblem(Problem.PATH, "https://chat.example.org/#top")
    }

    private fun assertProblem(expected: Problem, input: String, allowLocalCleartext: Boolean = false) {
        try {
            ServerAddress.parse(input, allowLocalCleartext)
            fail("accepted $input")
        } catch (e: ServerAddress.InvalidException) {
            assertEquals(input, expected, e.problem)
        }
    }
}
