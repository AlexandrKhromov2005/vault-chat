package io.github.alexandrkhromov2005.vaultchat

import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.IOException
import java.util.Collections

@RunWith(RobolectricTestRunner::class)
class PushTokensTest {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val settings = Settings(ApplicationProvider.getApplicationContext())
    private val server = ServerAddress.parse("https://chat.example.org", allowLocalCleartext = false)
    private val userId = "00000000-0000-4000-8000-0000000000aa"
    private val states = MutableStateFlow<SessionState>(SessionState.Restoring)

    private val sent: MutableList<Pair<SessionState.SignedIn, String>> = Collections.synchronizedList(mutableListOf())
    private var deletes = 0
    private var issued = 0

    /** What sending and deleting do; a test may hold them or make them fail. */
    private var onSend: suspend (SessionState.SignedIn) -> Unit = {}
    private var onDelete: suspend () -> Unit = {}

    @After
    fun tearDown() {
        scope.cancel()
    }

    private fun signedIn() = SessionState.SignedIn(server, userId, "alice")

    private fun tokens() = PushTokens(
        states,
        settings,
        token = { "synthetic-token-${++issued}" },
        delete = {
            deletes++
            onDelete()
        },
        send = { state, token ->
            sent += state to token
            onSend(state)
        },
    )

    private suspend fun waitFor(what: String, condition: () -> Boolean) {
        repeat(150) {
            if (condition()) return
            delay(20)
        }
        assertTrue(what, condition())
    }

    @Test
    fun everyLoginRegistersTheToken() = runBlocking {
        val first = signedIn()
        states.value = first
        scope.launch { tokens().follow() }

        waitFor("the login got no token") { sent.size == 1 }
        assertSame(first, sent.single().first)
    }

    @Test
    fun aNewLoginOfTheSameUserGetsATokenAfterARegistrationStillUnderWay() = runBlocking {
        val hold = CompletableDeferred<Unit>()
        onSend = { hold.await() }
        val first = signedIn()
        states.value = first
        scope.launch { tokens().follow() }
        waitFor("the login got no token") { sent.size == 1 }

        // Sign out and in again while the first registration has no answer yet.
        states.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        val second = signedIn()
        states.value = second
        hold.complete(Unit)

        waitFor("the new login got no token") { sent.size == 2 }
        assertSame(second, sent.last().first)
    }

    @Test
    fun signingOutDeletesTheLocalToken() = runBlocking {
        states.value = signedIn()
        scope.launch { tokens().follow() }
        waitFor("the login got no token") { sent.size == 1 }

        states.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)

        waitFor("the token stayed after signing out") { deletes == 1 }
    }

    @Test
    fun aDeleteThatFailsIsTriedAgainAtTheNextStartAndOnlyThen() = runBlocking {
        settings.pushTokenBound = true
        onDelete = { throw IOException("offline") }
        states.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        val firstStart = scope.launch { tokens().follow() }
        waitFor("no delete was tried") { deletes == 1 }
        delay(100)
        firstStart.cancel()

        onDelete = {}
        val secondStart = scope.launch { tokens().follow() }
        waitFor("the delete was not tried again") { deletes == 2 }
        delay(100)
        secondStart.cancel()

        // Done now: a third start has nothing to delete.
        scope.launch { tokens().follow() }
        delay(200)
        assertEquals(2, deletes)
    }

    @Test
    fun anInstallationThatNeverRegisteredDeletesNothing() = runBlocking {
        states.value = SessionState.SignedOut(SessionState.Reason.NONE)
        scope.launch { tokens().follow() }
        delay(200)
        assertEquals(0, deletes)
    }

    @Test
    fun theNextLoginWaitsForTheDeleteUnderWay() = runBlocking {
        settings.pushTokenBound = true
        val hold = CompletableDeferred<Unit>()
        onDelete = { hold.await() }
        states.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        scope.launch { tokens().follow() }
        waitFor("no delete was tried") { deletes == 1 }

        states.value = signedIn()
        delay(200)
        assertEquals("a token was read while the delete could still remove it", 0, issued)

        hold.complete(Unit)
        waitFor("the login got no token") { sent.size == 1 }
    }

    @Test
    fun aTokenFromFcmIsRegisteredOnlyWhileSignedIn() = runBlocking {
        states.value = SessionState.SignedOut(SessionState.Reason.NONE)
        val tokens = tokens()
        tokens.changed("from-fcm-1")
        assertEquals(0, sent.size)

        val current = signedIn()
        states.value = current
        tokens.changed("from-fcm-2")

        assertEquals(listOf(current to "from-fcm-2"), sent.toList())
    }

    @Test
    fun aFailedRegistrationDoesNotStopFollowing() = runBlocking {
        onSend = { throw IOException("offline") }
        states.value = signedIn()
        scope.launch { tokens().follow() }
        waitFor("the login got no token") { sent.size == 1 }

        onSend = {}
        states.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        states.value = signedIn()

        waitFor("following ended with the failure") { sent.size == 2 }
    }
}
