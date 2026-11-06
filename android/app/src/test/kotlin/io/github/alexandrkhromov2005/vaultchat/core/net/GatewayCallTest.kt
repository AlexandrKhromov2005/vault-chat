package io.github.alexandrkhromov2005.vaultchat.core.net

import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.SocketEffect
import okhttp3.Call
import okhttp3.EventListener
import okhttp3.OkHttpClient
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.io.File
import java.io.IOException
import java.io.InterruptedIOException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

class GatewayCallTest {
    private val server = MockWebServer()
    private lateinit var api: GatewayApi

    @Before
    fun setUp() {
        server.start()
        val http = OkHttpClient.Builder().callTimeout(5, TimeUnit.SECONDS).build()
        api = Gateway(http).api(ServerAddress.parse("http://127.0.0.1:${server.port}", allowLocalCleartext = true))
    }

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun readsTheErrorEnvelopeAndRetryAfter() {
        server.enqueue(
            MockResponse.Builder()
                .code(429)
                .addHeader("Retry-After", "7")
                .body("""{"error":{"code":"rate_limited","message":"too many requests"}}""")
                .build(),
        )
        val error = failure { api.login(LoginRequest("a@example.org", "password1")) }
        assertEquals(429, error.status)
        assertEquals("rate_limited", error.code)
        assertEquals(7L, error.retryAfterSeconds)
    }

    @Test
    fun anErrorWithoutTheEnvelopeKeepsItsStatus() {
        server.enqueue(MockResponse.Builder().code(502).body("<html>Bad Gateway</html>").build())
        val error = failure { api.login(LoginRequest("a@example.org", "password1")) }
        assertEquals(502, error.status)
        assertEquals(ApiException.HTTP_ERROR, error.code)
        assertNull(error.retryAfterSeconds)
    }

    @Test
    fun anOversizedErrorBodyIsNotParsed() {
        val padding = " ".repeat(20 * 1024)
        server.enqueue(
            MockResponse.Builder()
                .code(400)
                .addHeader("Retry-After", "-1")
                .body("""{"error":{"code":"invalid_argument","message":"x"}$padding}""")
                .build(),
        )
        val error = failure { api.login(LoginRequest("a@example.org", "password1")) }
        assertEquals(ApiException.HTTP_ERROR, error.code)
        assertNull(error.retryAfterSeconds)
    }

    @Test
    fun aMalformedSuccessIsAnInvalidResponse() {
        server.enqueue(MockResponse.Builder().code(200).body("<html>captive portal</html>").build())
        assertEquals(ApiException.INVALID_RESPONSE, failure { api.login(LoginRequest("a@example.org", "p")) }.code)

        // Valid JSON without the token fields.
        server.enqueue(MockResponse.Builder().code(200).body("""{"user_id":"u"}""").build())
        assertEquals(ApiException.INVALID_RESPONSE, failure { api.login(LoginRequest("a@example.org", "p")) }.code)
    }

    @Test
    fun noContentIsASuccess() = runBlocking {
        server.enqueue(MockResponse.Builder().code(204).build())
        callNoContent { api.logout(RefreshTokenRequest("refresh-0")) }
    }

    @Test
    fun anOversizedSuccessBodyIsRejectedWhileReading() {
        // Well-formed JSON, padded beyond the limit for API responses.
        val padding = "x".repeat(2 * 1024 * 1024)
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body("""{"user_id":"u","access_token":"a","refresh_token":"r","access_expires_at":"2026-10-27T12:15:00Z","pad":"$padding"}""")
                .build(),
        )
        assertEquals(ApiException.INVALID_RESPONSE, failure { api.login(LoginRequest("a@example.org", "p")) }.code)
    }

    @Test
    fun anUnreachableServerIsANetworkError() {
        server.close()
        val error = failure { api.login(LoginRequest("a@example.org", "password1")) }
        assertEquals(0, error.status)
        assertEquals(ApiException.NETWORK, error.code)
    }

    private val room = """{"room_id":"00000000-0000-4000-8000-000000000001","kind":"channel","name":"news",""" +
        """"owner_id":"00000000-0000-4000-8000-0000000000aa","private":false,"created_at":"2026-10-27T12:00:00Z"}"""

    /** A pooled connection that the server drops after reading the next request, without answering it. */
    private fun dropAfterNextRequest() {
        server.enqueue(MockResponse.Builder().code(200).body("""{"users":[]}""").build())
        runBlocking { call { api.searchUsers("Bearer t", "bo", 20) } }
        server.enqueue(MockResponse.Builder().onResponseStart(SocketEffect.CloseSocket()).build())
        server.enqueue(MockResponse.Builder().code(201).body(room).build())
    }

    @Test
    fun okHttpRepeatsARequestWhoseConnectionBroke() = runBlocking {
        // The behaviour NotRepeatable guards against: an ordinary request is
        // sent again, so a create would run twice.
        dropAfterNextRequest()
        call { api.openDirect("Bearer t", OpenDirectRequest("00000000-0000-4000-8000-0000000000bb")) }
        assertEquals(3, server.requestCount)
    }

    @Test
    fun aNotRepeatableRequestIsSentOnce() {
        dropAfterNextRequest()
        val error = failure { api.createChannel("Bearer t", CreateChannelRequest("news", private = false), NotRepeatable) }
        assertEquals(ApiException.NETWORK, error.code)
        assertEquals(2, server.requestCount)
    }

    @Test
    fun theCreateBodyIsSentAsJson() = runBlocking {
        server.enqueue(MockResponse.Builder().code(201).body(room).build())
        call { api.createChannel("Bearer t", CreateChannelRequest("news", private = false), NotRepeatable) }
        assertEquals("""{"name":"news","private":false}""", server.takeRequest().body?.utf8())
    }

    @Test
    fun noContentBecomesAUnitBody() = runBlocking {
        server.enqueue(MockResponse.Builder().code(204).build())
        assertEquals(Unit, call { api.leave("Bearer t", "00000000-0000-4000-8000-000000000001").noContent() }.body)
    }

    @Test
    fun anUploadOutOfTimeIsATimeoutInTheCause() {
        val file = File.createTempFile("upload", ".txt").apply { writeText("hello") }
        try {
            val http = OkHttpClient.Builder().callTimeout(200, TimeUnit.MILLISECONDS).build()
            val short = Gateway(http).api(ServerAddress.parse("http://127.0.0.1:${server.port}", allowLocalCleartext = true))
            val body = FileUpload(file, "a.txt", beforeEnd = { Thread.sleep(1_000) })
            val error = failure { short.uploadFile("Bearer t", "00000000-0000-4000-8000-000000000001", body) }
            assertEquals(ApiException.NETWORK, error.code)
            assertTrue("the cause was ${error.cause}", error.cause is InterruptedIOException)
        } finally {
            file.delete()
        }
    }

    private val fileId = "00000000-0000-4000-8005-000000000001"

    private fun transferApi(http: OkHttpClient = OkHttpClient()) =
        Gateway(http).transferApi(ServerAddress.parse("http://127.0.0.1:${server.port}", allowLocalCleartext = true))

    @Test
    fun closingATransferBodyStopsAReadBlockedOnTheNetwork() {
        val cancelled = CountDownLatch(1)
        val http = OkHttpClient.Builder()
            .eventListener(
                object : EventListener() {
                    override fun canceled(call: Call) = cancelled.countDown()
                },
            )
            .build()
        server.enqueue(MockResponse.Builder().body(Buffer().write(ByteArray(64 * 1024))).onResponseBody(SocketEffect.Stall).build())
        val body = runBlocking { transferApi(http).fileContent("Bearer t", fileId).body()!! }
        val ended = CountDownLatch(1)
        var failure: Throwable? = null
        thread(isDaemon = true) {
            try {
                val stream = body.byteStream()
                val buffer = ByteArray(8192)
                while (stream.read(buffer) >= 0) Unit
            } catch (e: Throwable) {
                failure = e
            } finally {
                ended.countDown()
            }
        }
        assertFalse("the read did not block", ended.await(500, TimeUnit.MILLISECONDS))

        body.close()
        assertTrue("the read did not end within a second", ended.await(1, TimeUnit.SECONDS))
        assertTrue("the call was not cancelled", cancelled.await(1, TimeUnit.SECONDS))
        assertTrue("the read ended with $failure", failure is IOException)
    }

    @Test
    fun aTransferBodyReadToItsEndKeepsItsConnection() = runBlocking {
        server.enqueue(MockResponse.Builder().body("first").build())
        server.enqueue(MockResponse.Builder().body("second").build())
        val api = transferApi()
        assertEquals("first", api.fileContent("Bearer t", fileId).body()!!.use { it.string() })
        assertEquals("second", api.fileContent("Bearer t", fileId).body()!!.use { it.string() })
        server.takeRequest()
        assertEquals(1, server.takeRequest().exchangeIndex)
    }

    private fun <T : Any> failure(request: suspend () -> retrofit2.Response<T>): ApiException = runBlocking {
        try {
            call(request)
            fail("request succeeded")
            throw AssertionError()
        } catch (e: ApiException) {
            e
        }
    }
}
