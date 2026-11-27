package io.github.alexandrkhromov2005.vaultchat.core.calls

import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatRequests
import io.github.alexandrkhromov2005.vaultchat.core.chat.Frame
import io.github.alexandrkhromov2005.vaultchat.core.net.Gateway
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.net.call
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.SocketEffect
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import retrofit2.Response
import java.util.UUID

class CallsTest {
    private val server = MockWebServer()
    private lateinit var requests: ChatRequests
    private lateinit var media: FakeCallMedia
    private lateinit var calls: Calls
    private val authorization = mutableListOf<String>()
    private val signalRequests = mutableListOf<String>()

    @Before
    fun setUp() {
        server.start()
        val http = OkHttpClient.Builder().addInterceptor { chain ->
            val request = chain.request()
            request.header("Authorization")?.let(authorization::add)
            if (request.method == "POST" && request.url.encodedPath.endsWith("/signal")) signalRequests += request.url.encodedPath
            chain.proceed(request)
        }.build()
        val api = Gateway(http).api(ServerAddress.parse("http://127.0.0.1:${server.port}", allowLocalCleartext = true))
        requests = object : ChatRequests {
            override suspend fun <T : Any> send(request: suspend GatewayApi.(String) -> Response<T>): T {
                return call { api.request("Bearer test") }.body
            }
        }
        media = FakeCallMedia()
        calls = Calls("alice", requests, mediaFactory = media)
    }

    @After
    fun tearDown() {
        calls.stop()
        server.close()
    }

    @Test
    fun calleeAcceptsOnlyAfterMicrophoneAndCleansUp() = runBlocking {
        val incoming = call("call-1", "ringing", caller = "bob", callee = "alice")
        enqueue("""{"calls":[$incoming]}""", call("call-1", "connecting", caller = "bob", callee = "alice"), turn(), call("call-1", "ended", caller = "bob", callee = "alice"))

        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        calls.accept()
        await("accepted") { calls.view.value?.state == "connecting" && calls.view.value?.owned == true }
        await("media") { media.started == 1 }
        assertEquals(1, media.prepared)
        calls.end()
        await("ended") { calls.view.value?.state == "ended" }
        assertEquals(1, media.closed)
        assertSingleBearerPrefix()
    }

    @Test
    fun foregroundStartsBeforeMediaInitialization() = runBlocking {
        calls = Calls("alice", requests, mediaFactory = media, foreground = { active ->
            media.events += if (active) "foreground-start" else "foreground-stop"
        })
        val incoming = call("call-1", "ringing", caller = "bob", callee = "alice")
        enqueue("""{"calls":[$incoming]}""", call("call-1", "connecting", caller = "bob", callee = "alice"), turn(), call("call-1", "ended", caller = "bob", callee = "alice"))

        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        calls.accept()
        await("media") { media.started == 1 }
        calls.end()
        await("ended") { calls.view.value?.state == "ended" }

        assertEquals(listOf("foreground-start", "media", "foreground-stop"), media.events)
    }

    @Test
    fun mediaInitializationFailureStopsForeground() = runBlocking {
        calls = Calls("alice", requests, mediaFactory = media, foreground = { active ->
            media.events += if (active) "foreground-start" else "foreground-stop"
        })
        media.startError = IllegalStateException("synthetic media failure")
        val incoming = call("call-1", "ringing", caller = "bob", callee = "alice")
        enqueue("""{"calls":[$incoming]}""", call("call-1", "connecting", caller = "bob", callee = "alice"), turn())

        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        calls.accept()
        await("ended") { calls.view.value?.state == "ended" }

        assertEquals(listOf("foreground-start", "media", "foreground-stop"), media.events)
    }

    @Test
    fun callerReconcilesSignalsWithoutDuplicatingMedia() = runBlocking {
        val id = UUID.randomUUID().toString().replace("-", "").uppercase()
        enqueue(
            call(id, "ringing"), call(id, "ringing"), call(id, "connecting"), turn(),
            call(id, "active"), """{"state":"connecting","signal_seq":1}""",
            """{"signals":[{"signal_seq":1,"sender_device":"device-alice","sender_seq":1,"kind":"offer","payload":"synthetic-offer"},{"signal_seq":2,"sender_device":"device-bob","sender_seq":1,"kind":"answer","payload":"synthetic-answer"}]}""",
            call(id, "ended"),
        )

        calls.start("room-1")
        await("created") { calls.view.value?.state == "ringing" }
        calls.onEvent(Frame.CallEvent("call.accepted", id))
        await("media") { media.started == 1 }
        media.listener!!.connected()
        media.listener!!.remote()
        media.listener!!.connection(CallConnection.RELAY)
        await("relay transport") { calls.view.value?.connection == CallConnection.RELAY }
        await("active") { calls.view.value?.state == "active" }
        media.listener!!.signal("offer", "synthetic-offer")
        calls.onEvent(Frame.CallEvent("call.signal", id))
        await("answer") { media.received.contains("answer") }
        assertEquals(1, media.started)
        assertEquals(listOf("answer"), media.received)
        calls.end()
        await("ended") { calls.view.value?.state == "ended" }
        assertEquals(1, media.closed)
        assertSingleBearerPrefix()
    }

    @Test
    fun calleeAnswersOfferDuringSignalCatchUp() = runBlocking {
        media.answerOnOffer = true
        enqueue(
            """{"calls":[${call("call-1", "ringing", caller = "bob", callee = "alice")}]}""",
            call("call-1", "connecting", caller = "bob", callee = "alice"),
            turn(),
            """{"signals":[{"signal_seq":1,"sender_device":"device-bob","sender_seq":1,"kind":"offer","payload":"synthetic-offer"}]}""",
            """{"state":"connecting","signal_seq":2}""",
            call("call-1", "ended", caller = "bob", callee = "alice"),
        )

        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        calls.accept()
        await("media") { media.started == 1 }
        calls.onEvent(Frame.CallEvent("call.signal", "call-1"))
        await("answer") { media.answerSent }
        assertEquals(listOf("offer"), media.received)
        calls.end()
        await("ended") { calls.view.value?.state == "ended" }
        assertSingleBearerPrefix()
    }

    @Test
    fun mediaStartCatchesUpPendingSignals() = runBlocking {
        val gate = CompletableDeferred<Unit>()
        media.startGate = gate
        enqueue(
            """{"calls":[${call("call-1", "ringing", caller = "bob", callee = "alice")}]}""",
            call("call-1", "connecting", caller = "bob", callee = "alice"),
            turn(),
            """{"signals":[{"signal_seq":1,"sender_device":"device-bob","sender_seq":1,"kind":"offer","payload":"synthetic-offer"}]}""",
            call("call-1", "ended", caller = "bob", callee = "alice"),
        )

        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        calls.accept()
        await("media start") { media.events.contains("media") }
        calls.onEvent(Frame.CallEvent("call.signal", "call-1"))
        await("pending signal fetched") { calls.rtcDiagnostics().contains("cursor=1") }
        gate.complete(Unit)

        await("pending offer") { media.received == listOf("offer") }
        calls.end()
        await("ended") { calls.view.value?.state == "ended" }
        assertSingleBearerPrefix()
    }

    @Test
    fun candidateSignalRetriesPastTransientOutage() = runBlocking {
        media.candidateOnStart = true
        enqueue(
            """{"calls":[${call("call-1", "ringing", caller = "bob", callee = "alice")}]}""",
            call("call-1", "connecting", caller = "bob", callee = "alice"),
            turn(),
        )
        repeat(10) {
            server.enqueue(MockResponse.Builder().code(500).body("""{"error":"synthetic outage"}""").build())
        }
        enqueue("""{"state":"connecting","signal_seq":1}""", call("call-1", "ended", caller = "bob", callee = "alice"))

        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        calls.accept()

        await("media after candidate retry", 300) { media.started == 1 }
        assertTrue("candidate was not retried past three failures: $signalRequests", signalRequests.size > 3)
        calls.end()
        await("ended") { calls.view.value?.state == "ended" }
        calls.dismiss()
        assertSingleBearerPrefix()
    }

    @Test
    fun serverEndedDuringLocalEndReleasesMedia() = runBlocking {
        enqueue(
            """{"calls":[${call("call-1", "ringing", caller = "bob", callee = "alice")}]}""",
            call("call-1", "connecting", caller = "bob", callee = "alice"),
            turn(),
        )
        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        calls.accept()
        await("media") { media.started == 1 }
        server.enqueue(MockResponse.Builder().code(404).body("""{"error":"ended"}""").build())

        calls.end()
        await("ended") { calls.view.value?.state == "ended" }
        assertEquals(1, media.closed)
        assertSingleBearerPrefix()
    }

    @Test
    fun networkRetryReusesCommandId() = runBlocking {
        val id = UUID.randomUUID().toString().replace("-", "").uppercase()
        server.enqueue(MockResponse.Builder().onResponseStart(SocketEffect.CloseSocket()).build())
        enqueue(call(id, "ringing"), call(id, "ringing"))

        calls.start("room-1")
        await("created after retry") { calls.view.value?.state == "ringing" }
        val first = server.takeRequest().body!!.utf8()
        val second = server.takeRequest().body!!.utf8()
        val command = """"command_id":"([^"]+)"""".toRegex()
        val firstId = command.find(first)!!.groupValues[1]
        assertEquals(firstId, command.find(second)!!.groupValues[1])
        assertSingleBearerPrefix()
    }

    @Test
    fun callerCancelsRingingCallWithoutNestedCommandLock() = runBlocking {
        enqueue(call("call-1", "ringing"), call("call-1", "ringing"), call("call-1", "ended"))

        calls.start("room-1")
        await("created") { calls.view.value?.state == "ringing" }
        calls.cancel()

        await("cancelled") { calls.view.value?.state == "ended" }
        assertEquals(1, media.closed)
        assertEquals(3, server.requestCount)
        assertSingleBearerPrefix()
    }

    @Test
    fun serverMissingCallDuringRefreshFinishesIt() = runBlocking {
        enqueue(call("call-1", "ringing"), call("call-1", "ringing"))
        calls.start("room-1")
        await("created") { calls.view.value?.state == "ringing" }
        server.enqueue(MockResponse.Builder().code(404).body("""{"error":"not_found"}""").build())

        calls.onEvent(Frame.CallEvent("call.ended", "call-1"))

        await("expired") { calls.view.value?.state == "ended" }
        assertEquals(1, media.closed)
        assertSingleBearerPrefix()
    }

    @Test
    fun expiredIncomingCallDoesNotBlockNewIncomingCall() = runBlocking {
        enqueue("""{"calls":[${call("old-call", "ringing", caller = "bob", callee = "alice")}]}""")
        calls.reconcile()
        await("old incoming") { calls.view.value?.callId == "old-call" }
        enqueue("""{"calls":[${call("new-call", "ringing", caller = "bob", callee = "alice")}]}""")
        server.enqueue(MockResponse.Builder().code(404).body("""{"error":"not_found"}""").build())

        calls.onEvent(Frame.CallEvent("call.created", "new-call"))

        await("new incoming") { calls.view.value?.callId == "new-call" && calls.view.value?.state == "ringing" }
        assertSingleBearerPrefix()
    }

    @Test
    fun acceptingExpiredIncomingCallReleasesPreparedMedia() = runBlocking {
        enqueue("""{"calls":[${call("call-1", "ringing", caller = "bob", callee = "alice")}]}""")
        calls.reconcile()
        await("incoming") { calls.view.value?.state == "ringing" }
        server.enqueue(MockResponse.Builder().code(404).body("""{"error":"not_found"}""").build())

        calls.accept()

        await("expired") { calls.view.value?.state == "ended" }
        assertEquals(1, media.prepared)
        assertEquals(1, media.closed)
        assertSingleBearerPrefix()
    }

    @Test
    fun permissionFailureDoesNotCreateCall() = runBlocking {
        media.prepareError = SecurityException("denied")
        calls.start("room-1")
        await("local rejection") { calls.view.value?.state == "ended" }
        assertEquals(0, server.requestCount)
        assertEquals(1, media.closed)
        assertSingleBearerPrefix()
    }

    private fun assertSingleBearerPrefix() {
        assertTrue(authorization.all { it == "Bearer test" })
    }

    private fun enqueue(vararg bodies: String) {
        bodies.forEach { body -> server.enqueue(MockResponse.Builder().body(body).build()) }
    }

    private suspend fun await(description: String, attempts: Int = 100, condition: suspend () -> Boolean) {
        repeat(attempts) {
            if (condition()) return
            delay(50)
        }
        throw AssertionError("Timed out: $description; view=${calls.view.value}")
    }

    private fun call(id: String, state: String, caller: String = "alice", callee: String = "bob") =
        """{"call_id":"$id","room_id":"room-1","state":"$state","state_version":1,"caller_id":"$caller","callee_id":"$callee"}"""

    private fun turn() = """{"ice_servers":[],"ttl":600}"""
}

private class FakeCallMedia : CallMediaFactory, CallMedia {
    var prepared = 0
    var started = 0
    var closed = 0
    var prepareError: Exception? = null
    var startError: Exception? = null
    var startGate: CompletableDeferred<Unit>? = null
    val events = mutableListOf<String>()
    var listener: CallMediaListener? = null
    var answerOnOffer = false
    var answerSent = false
    var candidateOnStart = false
    val received = mutableListOf<String>()

    override fun create(): CallMedia = this

    override suspend fun prepare() {
        prepareError?.let { throw it }
        prepared++
    }

    override suspend fun start(role: CallRole, iceServers: List<io.github.alexandrkhromov2005.vaultchat.core.net.IceServerDto>, listener: CallMediaListener) {
        events += "media"
        startError?.let { throw it }
        startGate?.await()
        this.listener = listener
        if (candidateOnStart) listener.signal("candidate", "synthetic-candidate")
        started++
    }

    override suspend fun receive(kind: String, payload: String) {
        if (started == 0) return
        received += kind
        if (kind == "offer" && answerOnOffer) {
            listener!!.signal("answer", "synthetic-answer")
            answerSent = true
        }
    }

    override fun close() {
        closed++
    }
}
