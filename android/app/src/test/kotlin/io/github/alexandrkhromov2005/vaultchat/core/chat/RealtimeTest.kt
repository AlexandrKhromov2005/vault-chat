package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

@OptIn(ExperimentalCoroutinesApi::class)
class RealtimeTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val room = "00000000-0000-4000-8000-000000000001"

    private class FakeSocket(private val listener: WebSocketListener) : WebSocket {
        val sent = mutableListOf<String>()
        var closedWith: Int? = null

        override fun request(): Request = Request.Builder().url("http://127.0.0.1/api/v1/ws").build()

        override fun queueSize(): Long = 0

        override fun send(text: String): Boolean {
            sent += text
            return true
        }

        override fun send(bytes: ByteString): Boolean = false

        override fun close(code: Int, reason: String?): Boolean {
            if (closedWith == null) closedWith = code
            return true
        }

        override fun cancel() = Unit

        private fun response(code: Int, retryAfter: String? = null) = Response.Builder()
            .request(request())
            .protocol(Protocol.HTTP_1_1)
            .code(code)
            .message("")
            .apply { if (retryAfter != null) header("Retry-After", retryAfter) }
            .build()

        fun open() = listener.onOpen(this, response(101))

        fun receive(text: String) = listener.onMessage(this, text)

        fun closeFromServer(code: Int) = listener.onClosed(this, code, "")

        fun fail(code: Int? = null, retryAfter: String? = null) =
            listener.onFailure(this, IOException("broken"), code?.let { response(it, retryAfter) })
    }

    /** Access tokens of [lifetimeMs], renewed when asked for more validity than they have. */
    private inner class Tokens(private val scope: TestScope, var lifetimeMs: Long = 15 * 60_000L) : AccessTokens {
        var issued = 0
        var current: AccessGrant? = null
        var failure: ApiException? = null
        val renewedAfter = mutableListOf<String>()

        private fun issue(): AccessGrant {
            failure?.let { throw it }
            issued++
            return AccessGrant("token-$issued", scope.testScheduler.currentTime + lifetimeMs).also { current = it }
        }

        override suspend fun grant(minValidityMs: Long): AccessGrant {
            val grant = current
            if (grant != null && grant.validUntil - scope.testScheduler.currentTime > minValidityMs) return grant
            return issue()
        }

        override suspend fun renew(rejected: String): AccessGrant {
            renewedAfter += rejected
            return issue()
        }
    }

    private class Harness(val realtime: Realtime, val sockets: List<FakeSocket>, val readies: () -> Int, val frames: List<Frame>, val job: Job)

    private fun TestScope.start(tokens: Tokens = Tokens(this)): Harness {
        val sockets = mutableListOf<FakeSocket>()
        val frames = mutableListOf<Frame>()
        var readies = 0
        val realtime = Realtime(
            userId = me,
            tokens = tokens,
            open = { listener -> FakeSocket(listener).also { sockets += it } },
            elapsed = { testScheduler.currentTime },
            // Always the full delay: 500, 1000, 2000, ...
            backoff = Backoff(random = { 1.0 }),
            onReady = { readies++ },
            onFrame = { frames += it },
        )
        val job = backgroundScope.launch { realtime.run() }
        runCurrent()
        return Harness(realtime, sockets, { readies }, frames, job)
    }

    private fun ready(user: String = me) = """{"type":"ready","user_id":"$user","expires_at":"2026-10-15T12:15:00Z"}"""

    private fun FakeSocket.goLive(scope: TestScope) {
        open()
        scope.runCurrent()
        receive(ready())
        scope.runCurrent()
    }

    private fun authToken(frame: String): String = Regex(""""token":"([^"]+)"""").find(frame)!!.groupValues[1]

    @Test
    fun theTokenGoesInTheFirstFrameAndEventsFollowReady() = runTest {
        val h = start()
        val socket = h.sockets.single()
        assertTrue(socket.sent.isEmpty())
        assertEquals(RealtimeState.CONNECTING, h.realtime.state.value)

        socket.open()
        runCurrent()
        assertEquals("""{"type":"auth","token":"token-1"}""", socket.sent.single())

        // Nothing before ready counts as an event.
        socket.receive("""{"type":"member.added","room_id":"$room","user_id":"$me"}""")
        socket.receive(ready())
        socket.receive("""{"type":"member.added","room_id":"$room","user_id":"$me"}""")
        socket.receive("""{"type":"unknown"}""")
        runCurrent()

        assertEquals(RealtimeState.LIVE, h.realtime.state.value)
        assertEquals(1, h.readies())
        assertEquals(listOf<Frame>(Frame.MemberAdded(room, me)), h.frames)
    }

    @Test
    fun theTokenIsRenewedOnTheConnectionBeforeItExpires() = runTest {
        val tokens = Tokens(this)
        val h = start(tokens)
        val socket = h.sockets.single()
        socket.goLive(this)

        advanceTimeBy(15 * 60_000L - Realtime.REAUTH_BEFORE_MS - 1)
        runCurrent()
        assertEquals(1, socket.sent.size)

        advanceTimeBy(2)
        runCurrent()
        assertEquals(listOf("token-1", "token-2"), socket.sent.map(::authToken))

        // The second ready renews again; it is not a new connection.
        socket.receive(ready())
        runCurrent()
        assertEquals(1, h.readies())
        assertNull(socket.closedWith)
    }

    @Test
    fun authFramesAreNeverSentLessThanTenSecondsApart() = runTest {
        // Tokens too short-lived for the renewal margin.
        val tokens = Tokens(this, lifetimeMs = 30_000L)
        val h = start(tokens)
        val socket = h.sockets.single()
        socket.goLive(this)

        advanceTimeBy(Realtime.MIN_REAUTH_INTERVAL_MS - 1)
        runCurrent()
        assertEquals(1, socket.sent.size)

        advanceTimeBy(1)
        runCurrent()
        assertEquals(2, socket.sent.size)
    }

    @Test
    fun aRejectedTokenIsRenewedAndTheConnectionReopensAtOnce() = runTest {
        val tokens = Tokens(this)
        val h = start(tokens)
        h.sockets[0].goLive(this)

        h.sockets[0].closeFromServer(4001)
        runCurrent()

        assertEquals(listOf("token-1"), tokens.renewedAfter)
        assertEquals(2, h.sockets.size)
        h.sockets[1].open()
        runCurrent()
        assertEquals("token-2", authToken(h.sockets[1].sent.single()))
    }

    @Test
    fun aRejectedRenewalEndsTheConnection() = runTest {
        val tokens = Tokens(this)
        val h = start(tokens)
        h.sockets[0].goLive(this)
        tokens.failure = ApiException(401, "unauthenticated", "session ended")

        h.sockets[0].closeFromServer(4001)
        advanceTimeBy(60_000)
        runCurrent()

        assertEquals(1, h.sockets.size)
        assertFalse(h.job.isActive)
        assertEquals(RealtimeState.STOPPED, h.realtime.state.value)
    }

    @Test
    fun theBackoffGrowsUntilAConnectionStaysUp() = runTest {
        val h = start()

        h.sockets[0].fail()
        runCurrent()
        assertEquals(RealtimeState.WAITING, h.realtime.state.value)
        advanceTimeBy(499)
        runCurrent()
        assertEquals(1, h.sockets.size)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(2, h.sockets.size)

        // Live, but dropped at once: the backoff keeps growing.
        h.sockets[1].goLive(this)
        h.sockets[1].closeFromServer(1013)
        runCurrent()
        advanceTimeBy(999)
        runCurrent()
        assertEquals(2, h.sockets.size)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(3, h.sockets.size)

        // Stable for a while: the next attempt starts over.
        h.sockets[2].goLive(this)
        advanceTimeBy(Realtime.STABLE_MS)
        h.sockets[2].closeFromServer(1001)
        runCurrent()
        advanceTimeBy(500)
        runCurrent()
        assertEquals(4, h.sockets.size)
    }

    @Test
    fun retryAfterOfARefusedUpgradeIsHonoured() = runTest {
        val h = start()

        h.sockets[0].fail(code = 503, retryAfter = "7")
        runCurrent()
        advanceTimeBy(6_999)
        runCurrent()
        assertEquals(1, h.sockets.size)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(2, h.sockets.size)
    }

    @Test
    fun clientErrorsStopReconnecting() = runTest {
        for (code in listOf(1003, 1008, 1009)) {
            val h = start()
            h.sockets[0].goLive(this)
            h.sockets[0].closeFromServer(code)
            advanceTimeBy(60_000)
            runCurrent()

            assertEquals(1, h.sockets.size)
            assertEquals(RealtimeState.FAILED, h.realtime.state.value)
            assertFalse(h.job.isActive)
        }
    }

    @Test
    fun aConnectionWithoutReadyIsDropped() = runTest {
        val h = start()
        h.sockets[0].open()
        runCurrent()

        advanceTimeBy(Realtime.CONNECT_TIMEOUT_MS - 1)
        runCurrent()
        assertNull(h.sockets[0].closedWith)

        advanceTimeBy(1)
        runCurrent()
        assertEquals(1000, h.sockets[0].closedWith)
        advanceTimeBy(500)
        runCurrent()
        assertEquals(2, h.sockets.size)
    }

    @Test
    fun aReadyForAnotherUserClosesTheConnection() = runTest {
        val h = start()
        h.sockets[0].open()
        runCurrent()
        h.sockets[0].receive(ready(user = "00000000-0000-4000-8000-0000000000bb"))
        h.sockets[0].receive("""{"type":"member.added","room_id":"$room","user_id":"$me"}""")
        runCurrent()

        assertEquals(0, h.readies())
        assertTrue(h.frames.isEmpty())
        assertEquals(1000, h.sockets[0].closedWith)
        assertEquals(RealtimeState.FAILED, h.realtime.state.value)
    }

    @Test
    fun aSessionThatEndedStopsTheConnection() = runTest {
        val tokens = Tokens(this)
        tokens.failure = ApiException(401, "unauthenticated", "not signed in")
        val h = start(tokens)
        h.sockets[0].open()
        runCurrent()

        assertTrue(h.sockets[0].sent.isEmpty())
        assertFalse(h.job.isActive)
        assertEquals(RealtimeState.STOPPED, h.realtime.state.value)
    }

    @Test
    fun aTokenThatCannotBeHadForNowIsRetried() = runTest {
        val tokens = Tokens(this)
        tokens.failure = ApiException(0, ApiException.NETWORK, "network failure")
        val h = start(tokens)
        h.sockets[0].open()
        runCurrent()
        tokens.failure = null

        advanceTimeBy(500)
        runCurrent()
        assertEquals(2, h.sockets.size)
    }

    @Test
    fun stoppingClosesTheSocket() = runTest {
        val h = start()
        h.sockets[0].goLive(this)

        h.job.cancel()
        runCurrent()

        assertEquals(1000, h.sockets[0].closedWith)
        assertEquals(RealtimeState.STOPPED, h.realtime.state.value)
    }
}
