package io.github.alexandrkhromov2005.vaultchat.core.net

import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

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
