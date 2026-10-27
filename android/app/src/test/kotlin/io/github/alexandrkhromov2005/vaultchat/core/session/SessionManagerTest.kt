package io.github.alexandrkhromov2005.vaultchat.core.session

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.Gateway
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import mockwebserver3.SocketEffect
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

class SessionManagerTest {
    private val server = MockWebServer()
    private val store = MemoryStore()
    private val requests: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val handlers = mutableMapOf<String, (RecordedRequest) -> MockResponse>()
    private val refreshes = AtomicInteger()

    // The server's clock; the device's clock is serverNow + skew.
    private val serverNow = Instant.parse("2026-10-27T12:00:00Z")
    private var deviceNow = serverNow.toEpochMilli()

    // The device's monotonic clock, which also runs in deep sleep.
    private var deviceElapsed = 1_000_000L
    private lateinit var address: ServerAddress
    private lateinit var gateway: Gateway
    private lateinit var manager: SessionManager

    @Before
    fun setUp() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.url.encodedPath
                requests += "$path ${request.body?.utf8().orEmpty()}"
                val handler = handlers[path] ?: return MockResponse.Builder().code(404).build()
                return handler(request)
            }
        }
        server.start()
        address = ServerAddress.parse("http://127.0.0.1:${server.port}", allowLocalCleartext = true)
        val http = OkHttpClient.Builder().callTimeout(5, TimeUnit.SECONDS).build()
        gateway = Gateway(http)
        manager = newManager()
        handlers["/api/v1/auth/refresh"] = { tokenPair(refreshes.incrementAndGet()) }
        handlers["/api/v1/auth/logout"] = { MockResponse.Builder().code(204).build() }
    }

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun concurrentCallersShareOneRefresh() = runBlocking {
        restoreSignedIn()
        handlers["/api/v1/auth/refresh"] = {
            Thread.sleep(300)
            tokenPair(refreshes.incrementAndGet())
        }
        val tokens = (1..5).map { async(Dispatchers.IO) { manager.accessToken() } }.awaitAll()

        assertEquals(setOf("access-1"), tokens.toSet())
        assertEquals(1, refreshes.get())
        assertEquals("refresh-1", store.session?.refreshToken)
    }

    @Test
    fun aRejectedRefreshSignsOut() = runBlocking {
        restoreSignedIn()
        handlers["/api/v1/auth/refresh"] = { error(401, "unauthenticated", "invalid token") }

        assertEquals(401, failure { manager.accessToken() }.status)
        assertEquals(SessionState.SignedOut(SessionState.Reason.EXPIRED), manager.state.value)
        assertNull(store.session)
    }

    @Test
    fun aNetworkFailureKeepsTheSession() = runBlocking {
        restoreSignedIn()
        server.close()

        assertEquals(ApiException.NETWORK, failure { manager.accessToken() }.code)
        assertTrue(manager.state.value is SessionState.SignedIn)
        assertEquals("refresh-0", store.session?.refreshToken)
    }

    @Test
    fun aServerErrorDuringRefreshKeepsTheSession() = runBlocking {
        restoreSignedIn()
        handlers["/api/v1/auth/refresh"] = { error(503, "unavailable", "auth unavailable") }

        assertEquals(503, failure { manager.accessToken() }.status)
        assertTrue(manager.state.value is SessionState.SignedIn)
        assertEquals("refresh-0", store.session?.refreshToken)
    }

    @Test
    fun aCancelledCallerStillStoresTheRotatedToken() = runBlocking {
        restoreSignedIn()
        val (started, release) = blockRefresh()
        val caller = async(Dispatchers.IO) { manager.accessToken() }
        assertTrue(started.await(5, TimeUnit.SECONDS))
        caller.cancel()
        release.countDown()

        // The server has retired refresh-0; losing refresh-1 would make the
        // next refresh look like token theft.
        withTimeout(5_000) { while (store.session?.refreshToken != "refresh-1") delay(10) }
        assertEquals("access-1", manager.accessToken())
        assertEquals(1, refreshes.get())
    }

    @Test
    fun logoutDuringARefreshDropsItsAnswerAndRevokesAfterIt() = runBlocking {
        restoreSignedIn()
        val (started, release) = blockRefresh()
        val caller = async(Dispatchers.IO) { runCatching { manager.accessToken() } }
        assertTrue(started.await(5, TimeUnit.SECONDS))

        val logout = async(Dispatchers.IO, start = CoroutineStart.UNDISPATCHED) { manager.logout() }
        // Signed out locally at once; the server hears of it after the refresh.
        withTimeout(5_000) { while (manager.state.value !is SessionState.SignedOut) delay(10) }
        assertNull(store.session)
        delay(200)
        assertTrue(requests.none { it.startsWith("/api/v1/auth/logout") })

        release.countDown()
        logout.await()
        val result = caller.await()

        assertEquals(401, (result.exceptionOrNull() as ApiException).status)
        assertNull(store.session)
        assertEquals(SessionState.SignedOut(SessionState.Reason.LOGOUT), manager.state.value)
        assertEquals(
            listOf("/api/v1/auth/refresh", "/api/v1/auth/logout"),
            requests.map { it.substringBefore(' ') },
        )
        assertTrue(requests.last().contains("refresh-0"))
    }

    @Test
    fun aSkewedDeviceClockNeitherRefreshesEveryCallNorKeepsExpiredTokens() = runBlocking {
        // The phone's clock is an hour ahead of the server's.
        deviceNow = serverNow.toEpochMilli() + 3_600_000
        restoreSignedIn()

        assertEquals("access-1", manager.accessToken())
        assertEquals("access-1", manager.accessToken())
        assertEquals(1, refreshes.get())

        // 15 minutes of lifetime, renewed during the last minute.
        advance(13 * 60_000)
        assertEquals("access-1", manager.accessToken())
        advance(60_000 + 1)
        assertEquals("access-2", manager.accessToken())
        assertEquals(2, refreshes.get())
    }

    @Test
    fun aRejectedAccessTokenIsRenewedOnceAndNotInALoop() = runBlocking {
        restoreSignedIn()
        handlers["/api/v1/auth/me"] = { error(401, "unauthenticated", "invalid token") }

        val error = failure { manager.authorized { me(it) } }
        assertEquals(401, error.status)
        assertEquals(2, requests.count { it.startsWith("/api/v1/auth/me") })
        assertEquals(2, refreshes.get())
        // Only Auth's rejection of the refresh token ends the session.
        assertTrue(manager.state.value is SessionState.SignedIn)
    }

    @Test
    fun aRequestForAnEndedSessionIsNotSentWithTheNextSessionsToken() = runBlocking {
        restoreSignedIn()
        val first = manager.state.value as SessionState.SignedIn
        manager.logout()
        handlers["/api/v1/auth/login"] = { tokenPair(5) }
        handlers["/api/v1/auth/me"] = { user() }
        manager.login(address, "alice@example.org", "password1")
        requests.clear()

        // The same user signed in again, but in a new session: work started
        // for the old one must not go on with the new token.
        assertEquals(401, failure { manager.authorized(first) { me(it) } }.status)
        assertEquals(401, failure { manager.accessGrant(first, 0) }.status)
        assertEquals(401, failure { manager.renewAccess(first, "access-5") }.status)
        assertTrue(requests.isEmpty())

        val current = manager.state.value as SessionState.SignedIn
        assertEquals("alice", manager.authorized(current) { me(it) }.body.username)
    }

    @Test
    fun aGrantIsRenewedWhenItWouldNotLastLongEnough() = runBlocking {
        restoreSignedIn()
        val session = manager.state.value as SessionState.SignedIn

        val first = manager.accessGrant(session, 70_000)
        assertEquals("access-1", first.token)
        assertEquals(deviceElapsed + 15 * 60_000, first.validUntil)

        advance(15 * 60_000 - 71_000)
        assertEquals("access-1", manager.accessGrant(session, 70_000).token)
        advance(1_000)
        assertEquals("access-2", manager.accessGrant(session, 70_000).token)
        assertEquals(2, refreshes.get())
    }

    @Test
    fun aRejectedGrantIsRenewedOnlyOnce() = runBlocking {
        restoreSignedIn()
        val session = manager.state.value as SessionState.SignedIn
        val first = manager.accessGrant(session, 0)

        assertEquals("access-2", manager.renewAccess(session, first.token).token)
        // A late rejection of the old token does not rotate again.
        assertEquals("access-2", manager.renewAccess(session, first.token).token)
        assertEquals(2, refreshes.get())
    }

    @Test
    fun loginStoresTheSession() = runBlocking {
        manager.restore()
        handlers["/api/v1/auth/login"] = { tokenPair(0) }
        handlers["/api/v1/auth/me"] = { user() }

        manager.login(address, "alice@example.org", "password1")

        assertEquals(SessionState.SignedIn(address, "user-1", "alice"), manager.state.value)
        assertEquals(StoredSession(address.toString(), "user-1", "alice", "refresh-0"), store.session)
        assertEquals("access-0", manager.accessToken())
    }

    @Test
    fun aLoginWhoseProfileFailsIsRevoked() = runBlocking {
        manager.restore()
        handlers["/api/v1/auth/login"] = { tokenPair(0) }
        handlers["/api/v1/auth/me"] = { error(503, "unavailable", "chat unavailable") }

        assertEquals(503, failure { manager.login(address, "alice@example.org", "password1") }.status)
        assertTrue(manager.state.value is SessionState.SignedOut)
        assertNull(store.session)
        assertTrue(requests.last().startsWith("/api/v1/auth/logout") && requests.last().contains("refresh-0"))
    }

    @Test
    fun restoreDiscardsASessionForAServerThisBuildMayNotUse() = runBlocking {
        store.session = StoredSession("http://chat.example.org", "user-1", "alice", "refresh-0")

        manager.restore()

        assertEquals(SessionState.SignedOut(SessionState.Reason.NONE), manager.state.value)
        assertNull(store.session)
        assertTrue(requests.isEmpty())
    }

    @Test
    fun logoutWithoutAServerStillSignsOut() = runBlocking {
        restoreSignedIn()
        server.close()

        manager.logout()

        assertEquals(SessionState.SignedOut(SessionState.Reason.LOGOUT), manager.state.value)
        assertNull(store.session)
        assertEquals(401, failure { manager.accessToken() }.status)
    }

    // Review findings: a refresh whose outcome is unknown must not leave a
    // retired token that is presented after the server's retry window.

    @Test
    fun aLostRefreshResponseIsRetriedAtOnce() = runBlocking {
        val auth = FakeAuth(lose = 1)
        restoreSignedIn()

        assertEquals("access-1", manager.accessToken())
        assertEquals(listOf("refresh-0", "refresh-0"), auth.presented)
        assertEquals(false, auth.revokedAll)
        assertEquals("refresh-1", store.session?.refreshToken)
        assertNull(store.session?.refreshPendingSince)
    }

    @Test
    fun aRefreshLostBeyondTheRetryWindowEndsOnlyThisDevice() = runBlocking {
        val auth = FakeAuth(lose = Int.MAX_VALUE)
        restoreSignedIn()
        assertEquals(ApiException.NETWORK, failure { manager.accessToken() }.code)
        assertTrue(manager.state.value is SessionState.SignedIn)

        advance(60_000)
        val presentedBefore = auth.presented.size
        assertEquals(401, failure { manager.accessToken() }.status)

        assertEquals(presentedBefore, auth.presented.size)
        assertEquals(false, auth.revokedAll)
        assertTrue(auth.loggedOut)
        assertEquals(SessionState.SignedOut(SessionState.Reason.EXPIRED), manager.state.value)
        assertNull(store.session)
    }

    @Test
    fun aGatewayTimeoutAfterRotationIsAnUnknownOutcome() = runBlocking {
        val auth = FakeAuth(lose = Int.MAX_VALUE, lostAs = { error(504, "timeout", "request timed out") })
        restoreSignedIn()
        assertEquals(504, failure { manager.accessToken() }.status)

        advance(60_000)
        assertEquals(401, failure { manager.accessToken() }.status)
        assertEquals(false, auth.revokedAll)
        assertEquals(SessionState.SignedOut(SessionState.Reason.EXPIRED), manager.state.value)
    }

    @Test
    fun aFailureBeforeTheServerProcessedTheRefreshKeepsTheToken() = runBlocking {
        restoreSignedIn()
        // Gateway rate-limits before it calls Auth.
        handlers["/api/v1/auth/refresh"] = { error(429, "rate_limited", "too many requests") }
        assertEquals(429, failure { manager.accessToken() }.status)
        assertNull(store.session?.refreshPendingSince)

        advance(60_000)
        val auth = FakeAuth()
        assertEquals("access-1", manager.accessToken())
        assertEquals(listOf("refresh-0"), auth.presented)
        assertEquals(false, auth.revokedAll)
    }

    @Test
    fun aRotatedTokenThatCouldNotBeStoredIsNotPresentedAfterARestart() = runBlocking {
        val auth = FakeAuth()
        restoreSignedIn()
        store.failSavesOf = "refresh-1"
        assertEquals("access-1", manager.accessToken())

        // The process dies; the disk still has the retired refresh-0.
        advance(60_000)
        manager = newManager()
        manager.restore()
        failure { manager.accessToken() }

        assertEquals(listOf("refresh-0"), auth.presented)
        assertEquals(false, auth.revokedAll)
        assertTrue(manager.state.value is SessionState.SignedOut)
    }

    @Test
    fun aTokenThatCouldNotBeMarkedPendingIsNotLeftOnDisk() = runBlocking {
        val auth = FakeAuth(lose = Int.MAX_VALUE)
        restoreSignedIn()
        store.failPendingSaves = true
        assertEquals(ApiException.NETWORK, failure { manager.accessToken() }.code)

        // The process dies before the outcome is known.
        advance(60_000)
        manager = newManager()
        manager.restore()

        assertTrue(manager.state.value is SessionState.SignedOut)
        assertEquals(listOf("refresh-0", "refresh-0"), auth.presented)
        assertEquals(false, auth.revokedAll)
    }

    // Second review round.

    @Test
    fun aRefreshThatNeverLeftTheDeviceKeepsTheSession() = runBlocking {
        // Every connection attempt times out, as on a dead mobile network.
        val offline = OkHttpClient.Builder().socketFactory(TimingOutSockets()).build()
        manager = newManager(Gateway(offline))
        restoreSignedIn()
        assertEquals(ApiException.NETWORK, failure { manager.accessToken() }.code)

        advance(60_000)
        assertEquals(ApiException.NETWORK, failure { manager.accessToken() }.code)
        assertTrue(manager.state.value is SessionState.SignedIn)
        assertEquals("refresh-0", store.session?.refreshToken)
        assertNull(store.session?.refreshPendingSince)
    }

    @Test
    fun aClockSetBackDoesNotExtendTheRetryWindow() = runBlocking {
        val auth = FakeAuth(lose = Int.MAX_VALUE)
        restoreSignedIn()
        failure { manager.accessToken() }
        val presented = auth.presented.size

        // A minute passes while the wall clock is set back an hour.
        deviceNow -= 3_600_000
        deviceElapsed += 60_000
        failure { manager.accessToken() }

        assertEquals(presented, auth.presented.size)
        assertTrue(auth.loggedOut)
        assertEquals(false, auth.revokedAll)
    }

    @Test
    fun aSmallClockStepBackDoesNotExtendTheRetryWindow() = runBlocking {
        val auth = FakeAuth(lose = Int.MAX_VALUE)
        restoreSignedIn()
        failure { manager.accessToken() }
        val presented = auth.presented.size

        // 16 s pass, but NTP steps the wall clock back by 10 s meanwhile.
        deviceNow += 6_000
        deviceElapsed += 16_000
        failure { manager.accessToken() }

        assertEquals(presented, auth.presented.size)
        assertTrue(auth.loggedOut)
    }

    @Test
    fun aStoredPendingMarkFromTheFutureIsNotTrusted() = runBlocking {
        val auth = FakeAuth()
        store.session = StoredSession(address.toString(), "user-1", "alice", "refresh-0", refreshPendingSince = deviceNow + 3_600_000)
        manager.restore()
        failure { manager.accessToken() }

        assertTrue(auth.presented.isEmpty())
        assertTrue(auth.loggedOut)
    }

    @Test
    fun aRepeatIsNotStartedTooLateForTheServerWindow() = runBlocking {
        val auth = FakeAuth(lose = Int.MAX_VALUE)
        restoreSignedIn()
        failure { manager.accessToken() }
        val presented = auth.presented.size

        // A repeat started now could reach Auth after its 30 s window.
        advance(16_000)
        failure { manager.accessToken() }

        assertEquals(presented, auth.presented.size)
        assertTrue(auth.loggedOut)
    }

    @Test
    fun aLoginThatCannotBeStoredFailsWithAStorageError() = runBlocking {
        manager.restore()
        handlers["/api/v1/auth/login"] = { tokenPair(0) }
        handlers["/api/v1/auth/me"] = { user() }
        store.failSavesOf = "refresh-0"

        val error = runCatching { manager.login(address, "alice@example.org", "password1") }.exceptionOrNull()

        assertEquals("SessionStorageException", error!!::class.simpleName)
        assertTrue(manager.state.value is SessionState.SignedOut)
        assertTrue(requests.last().startsWith("/api/v1/auth/logout"))
    }

    @Test
    fun aRefreshIsNotSentWhenTheDiskCanNeitherMarkNorForgetTheToken() = runBlocking {
        val auth = FakeAuth()
        restoreSignedIn()
        store.failPendingSaves = true
        store.failClears = true

        failure { manager.accessToken() }

        assertTrue(auth.presented.isEmpty())
        assertEquals("refresh-0", store.session?.refreshToken)
    }

    @Test
    fun aMalformedRefreshTokenIsAlsoEndedOnTheServer() = runBlocking {
        restoreSignedIn()
        handlers["/api/v1/auth/refresh"] = { error(400, "invalid_argument", "invalid refresh token") }

        failure { manager.accessToken() }

        assertTrue(requests.last().startsWith("/api/v1/auth/logout"))
    }

    @Test
    fun aRetriedRequestIsNotSentUnderAnotherSession() = runBlocking {
        handlers["/api/v1/auth/login"] = { tokenPair(10) }
        val meStarted = CountDownLatch(1)
        val meRelease = CountDownLatch(1)
        handlers["/api/v1/auth/me"] = { request ->
            if (request.headers["Authorization"] == "Bearer access-1") {
                meStarted.countDown()
                meRelease.await(5, TimeUnit.SECONDS)
                error(401, "unauthenticated", "session revoked")
            } else {
                user()
            }
        }
        restoreSignedIn()
        val request = async(Dispatchers.IO) { runCatching { manager.authorized { me(it) } } }
        assertTrue(meStarted.await(5, TimeUnit.SECONDS))
        manager.logout()
        manager.login(address, "bob@example.org", "password1")
        meRelease.countDown()

        assertEquals(401, (request.await().exceptionOrNull() as ApiException).status)
        // The blocked /me and the one of the new login; none retried with its token.
        assertEquals(2, requests.count { it.startsWith("/api/v1/auth/me") })
        assertTrue(manager.state.value is SessionState.SignedIn)
    }

    @Test
    fun aRefreshRejectedAsMalformedEndsTheSession() = runBlocking {
        restoreSignedIn()
        handlers["/api/v1/auth/refresh"] = { error(400, "invalid_argument", "invalid refresh token") }

        assertEquals(400, failure { manager.accessToken() }.status)
        assertEquals(SessionState.SignedOut(SessionState.Reason.EXPIRED), manager.state.value)
        assertNull(store.session)
    }

    @Test
    fun aLoginAnsweredForAnotherUserIsRejected() = runBlocking {
        manager.restore()
        handlers["/api/v1/auth/login"] = { tokenPair(0) }
        handlers["/api/v1/auth/me"] = {
            MockResponse.Builder().code(200).body("""{"user_id":"user-2","username":"mallory"}""").build()
        }

        assertEquals(ApiException.INVALID_RESPONSE, failure { manager.login(address, "alice@example.org", "password1") }.code)
        assertTrue(manager.state.value is SessionState.SignedOut)
        assertNull(store.session)
        assertTrue(requests.last().startsWith("/api/v1/auth/logout"))
    }

    private fun newManager(gateway: Gateway = this.gateway) =
        SessionManager(store, gateway, allowLocalCleartext = true, now = { deviceNow }, elapsed = { deviceElapsed })

    /** Lets time pass on both device clocks. */
    private fun advance(millis: Long) {
        deviceNow += millis
        deviceElapsed += millis
    }

    /**
     * Auth's rotation as in internal/auth/service/session.go with the retry
     * window of PR #108: the token before the current one may be presented
     * again for 30 s; any other reuse revokes every session of the user.
     * The first [lose] answers are lost after the rotation is committed.
     */
    private inner class FakeAuth(
        private var lose: Int = 0,
        private val lostAs: () -> MockResponse = {
            MockResponse.Builder().onResponseStart(SocketEffect.ShutdownConnection).build()
        },
    ) {
        val presented: MutableList<String> = Collections.synchronizedList(mutableListOf())

        @Volatile
        var revokedAll = false

        @Volatile
        var loggedOut = false
        private var current = 0
        private var rotatedAt = 0L
        private var revoked = false

        init {
            handlers["/api/v1/auth/refresh"] = { request -> synchronized(this) { refresh(request) } }
            handlers["/api/v1/auth/logout"] = {
                synchronized(this) {
                    revoked = true
                    loggedOut = true
                }
                MockResponse.Builder().code(204).build()
            }
        }

        private fun refresh(request: RecordedRequest): MockResponse {
            val token = Regex("refresh-(\\d+)").find(request.body!!.utf8())!!.groupValues[1].toInt()
            presented += "refresh-$token"
            val answer = when {
                revoked -> return error(401, "unauthenticated", "invalid token")
                token == current -> {
                    current++
                    rotatedAt = deviceNow
                    tokenPair(current)
                }
                token == current - 1 && deviceNow - rotatedAt <= 30_000 -> tokenPair(current)
                else -> {
                    revoked = true
                    revokedAll = true
                    return error(401, "unauthenticated", "invalid token")
                }
            }
            if (lose > 0) {
                lose--
                return lostAs()
            }
            return answer
        }
    }

    private suspend fun restoreSignedIn() {
        store.session = StoredSession(address.toString(), "user-1", "alice", "refresh-0")
        manager.restore()
        assertTrue(manager.state.value is SessionState.SignedIn)
    }

    /** Makes the next refresh wait; returns (request received, let it answer). */
    private fun blockRefresh(): Pair<CountDownLatch, CountDownLatch> {
        val started = CountDownLatch(1)
        val release = CountDownLatch(1)
        handlers["/api/v1/auth/refresh"] = {
            started.countDown()
            release.await(5, TimeUnit.SECONDS)
            tokenPair(refreshes.incrementAndGet())
        }
        return started to release
    }

    private fun tokenPair(n: Int) = MockResponse.Builder()
        .code(200)
        .addHeader("Date", DateTimeFormatter.RFC_1123_DATE_TIME.format(serverNow.atOffset(ZoneOffset.UTC)))
        .body(
            """{"user_id":"user-1","token_type":"Bearer","access_token":"access-$n","refresh_token":"refresh-$n",""" +
                """"access_expires_at":"${serverNow.plusSeconds(15 * 60)}","refresh_expires_at":"2026-11-26T12:00:00Z"}""",
        )
        .build()

    private fun user() = MockResponse.Builder()
        .code(200)
        .body("""{"user_id":"user-1","email":"alice@example.org","username":"alice","expires_at":"2026-10-27T12:15:00Z"}""")
        .build()

    private fun error(status: Int, code: String, message: String) = MockResponse.Builder()
        .code(status)
        .body("""{"error":{"code":"$code","message":"$message"}}""")
        .build()

    private suspend fun failure(block: suspend () -> Any): ApiException {
        try {
            block()
        } catch (e: ApiException) {
            return e
        }
        fail("no failure")
        throw AssertionError()
    }
}

private class MemoryStore : SessionStore {
    @Volatile
    var session: StoredSession? = null

    /** A refresh token whose save fails, as when the disk is full. */
    @Volatile
    var failSavesOf: String? = null

    override suspend fun load(): StoredSession? = session

    /** Saves that mark a refresh as pending fail. */
    @Volatile
    var failPendingSaves = false

    @Volatile
    var failClears = false

    override suspend fun save(session: StoredSession) {
        if (session.refreshToken == failSavesOf) throw java.io.IOException("no space left on device")
        if (failPendingSaves && session.refreshPendingSince != null) throw java.io.IOException("no space left on device")
        this.session = session
    }

    override suspend fun clear() {
        if (failClears) throw java.io.IOException("read-only file system")
        session = null
    }
}

/** Sockets whose connect always times out. */
private class TimingOutSockets : javax.net.SocketFactory() {
    override fun createSocket(): java.net.Socket = object : java.net.Socket() {
        override fun connect(endpoint: java.net.SocketAddress?, timeout: Int) {
            throw java.net.SocketTimeoutException("connect timed out")
        }
    }

    override fun createSocket(host: String?, port: Int) = throw UnsupportedOperationException()

    override fun createSocket(host: String?, port: Int, localHost: java.net.InetAddress?, localPort: Int) =
        throw UnsupportedOperationException()

    override fun createSocket(host: java.net.InetAddress?, port: Int) = throw UnsupportedOperationException()

    override fun createSocket(address: java.net.InetAddress?, port: Int, localAddress: java.net.InetAddress?, localPort: Int) =
        throw UnsupportedOperationException()
}
