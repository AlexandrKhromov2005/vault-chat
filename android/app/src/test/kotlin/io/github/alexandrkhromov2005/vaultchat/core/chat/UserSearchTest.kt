package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.PublicProfile
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class UserSearchTest {
    private val sent = mutableListOf<String>()

    // advanceUntilIdle does not run work of backgroundScope; this scope's
    // work counts, and it is cancelled at the end of each test.
    private val scopes = mutableListOf<CoroutineScope>()

    private fun TestScope.newSearch(search: suspend (String) -> List<PublicProfile>): UserSearch {
        val scope = CoroutineScope(StandardTestDispatcher(testScheduler))
        scopes += scope
        return UserSearch(scope, search)
    }

    @After
    fun tearDown() {
        scopes.forEach { it.cancel() }
    }

    private fun user(name: String) = PublicProfile("00000000-0000-4000-8000-%012d".format(name.hashCode().toLong() and 0xffffff), name)

    @Test
    fun queriesTheServerRejectsSendNothing() = runTest {
        val search = newSearch({ sent += it; emptyList() })
        for (text in listOf("", "b", " b ", "1bob", "bo b", "боб", "b".repeat(33))) {
            search.onInput(text)
            advanceUntilIdle()
        }
        assertTrue(sent.isEmpty())
        assertEquals(UserQuery.Problem.INVALID, search.state.value.problem)
    }

    @Test
    fun typingSendsOnlyTheLastQuery() = runTest {
        val search = newSearch({ sent += it; listOf(user(it)) })
        for (text in listOf("bo", "bob", "bobb")) {
            search.onInput(text)
            advanceTimeBy(UserSearch.DELAY_MS - 1)
        }
        advanceUntilIdle()

        assertEquals(listOf("bobb"), sent)
        assertEquals(listOf("bobb"), search.state.value.users.map { it.username })
        assertFalse(search.state.value.searching)
    }

    @Test
    fun surroundingSpacesAreNotANewQuery() = runTest {
        val search = newSearch({ sent += it; emptyList() })
        search.onInput("bob")
        advanceUntilIdle()
        search.onInput(" bob ")
        advanceUntilIdle()
        assertEquals(listOf("bob"), sent)
    }

    @Test
    fun aNewerQueryCancelsAnOlderAnswer() = runTest {
        val slow = CompletableDeferred<List<PublicProfile>>()
        var cancelled = false
        val search = newSearch({ query ->
            sent += query
            if (query != "bo") return@newSearch listOf(user(query))
            try {
                slow.await()
            } catch (e: CancellationException) {
                cancelled = true
                throw e
            }
        })
        search.onInput("bo")
        advanceTimeBy(UserSearch.DELAY_MS + 1)
        runCurrent()
        assertEquals(listOf("bo"), sent)

        search.onInput("bob")
        advanceUntilIdle()
        assertTrue(cancelled)
        slow.complete(listOf(user("bo")))
        advanceUntilIdle()

        assertEquals("bob", search.state.value.query)
        assertEquals(listOf("bob"), search.state.value.users.map { it.username })
    }

    @Test
    fun anErrorIsShownAndRetried() = runTest {
        var fail = true
        val search = newSearch({ query ->
            sent += query
            if (fail) throw ApiException(0, ApiException.NETWORK, "down") else listOf(user(query))
        })
        search.onInput("bob")
        advanceUntilIdle()
        assertEquals(ApiException.NETWORK, search.state.value.error?.code)

        fail = false
        search.retry()
        advanceUntilIdle()

        assertNull(search.state.value.error)
        assertEquals(listOf("bob", "bob"), sent)
        assertEquals(1, search.state.value.users.size)
    }
}
