package io.github.alexandrkhromov2005.vaultchat.ui.auth

import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.Settings
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.Gateway
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionManager
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionStore
import io.github.alexandrkhromov2005.vaultchat.core.session.StoredSession
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.Collections
import java.util.concurrent.TimeUnit

/** Sign-in and registration against a server with email verification (Gateway after #105). */
@RunWith(RobolectricTestRunner::class)
class AuthViewModelTest {
    private val server = MockWebServer()
    private val requests: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val handlers = mutableMapOf<String, (RecordedRequest) -> MockResponse>()
    private val store = MemoryStore()
    private lateinit var session: SessionManager
    private lateinit var model: AuthViewModel

    @Before
    fun setUp() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.url.encodedPath
                requests += path
                return handlers[path]?.invoke(request) ?: MockResponse.Builder().code(404).build()
            }
        }
        server.start()
        val http = OkHttpClient.Builder().callTimeout(5, TimeUnit.SECONDS).build()
        session = SessionManager(store, Gateway(http), allowLocalCleartext = true, now = System::currentTimeMillis, elapsed = { System.nanoTime() / 1_000_000 })
        model = AuthViewModel(
            session,
            Settings(ApplicationProvider.getApplicationContext()),
            allowLocalCleartext = true,
            defaultServer = "http://127.0.0.1:${server.port}",
        )
        handlers["/api/v1/auth/login"] = { error(403, "email_verification_required", "email verification required") }
    }

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun aRegistrationThatRequiresEmailVerificationDoesNotSignIn() {
        handlers["/api/v1/auth/register"] = {
            json(201, """{"user_id":"$USER","email":"alice@example.org","username":"alice","verification_required":true}""")
        }

        model.register("alice@example.org", "alice", "password1", "a".repeat(64))
        settle()

        assertEquals(listOf("/api/v1/auth/register"), requests.toList())
        assertNull(model.error)
        assertTrue(session.state.value !is SessionState.SignedIn)
        assertEquals(AuthNotice.CONFIRM_EMAIL, model.notice)
        assertEquals("alice@example.org", model.createdEmail)
    }

    @Test
    fun aRegistrationWithoutVerificationSignsIn() {
        handlers["/api/v1/auth/register"] = { json(201, """{"user_id":"$USER","email":"alice@example.org","username":"alice"}""") }
        handlers["/api/v1/auth/login"] = { tokenPair() }
        handlers["/api/v1/auth/me"] = { json(200, """{"user_id":"$USER","email":"alice@example.org","username":"alice"}""") }

        model.register("alice@example.org", "alice", "password1", "a".repeat(64))
        settle()

        assertEquals(listOf("/api/v1/auth/register", "/api/v1/auth/login", "/api/v1/auth/me"), requests.toList())
        assertNull(model.error)
        assertNull(model.notice)
        assertTrue(session.state.value is SessionState.SignedIn)
    }

    @Test
    fun anUnverifiedEmailIsExplainedOnSignIn() {
        model.login("alice@example.org", "password1")
        settle()

        val error = model.error
        assertTrue(error is AuthError.Api && error.error.code == "email_verification_required")
        assertNotEquals(R.string.error_unexpected, describe(error!!, registering = false).id)
        assertEquals(R.string.error_email_unconfirmed, describe(error, registering = false).id)
        assertTrue(error.awaitsConfirmation)
        assertTrue(session.state.value !is SessionState.SignedIn)
    }

    @Test
    fun anotherConfirmationEmailCanBeRequested() {
        val bodies = Collections.synchronizedList(mutableListOf<String>())
        handlers["/api/v1/auth/email/verification/request"] = {
            bodies += it.body!!.utf8()
            json(202, """{"message":"If the request is eligible, an email will be sent."}""")
        }
        model.login("alice@example.org", "password1")
        settle()

        model.requestConfirmation(" alice@example.org ", "password1")
        settle()

        assertEquals("""{"email":"alice@example.org","password":"password1"}""", bodies.single())
        assertNull(model.error)
        assertEquals(AuthNotice.CONFIRMATION_REQUESTED, model.notice)
    }

    @Test
    fun aLimitedConfirmationRequestIsReported() {
        handlers["/api/v1/auth/email/verification/request"] = {
            MockResponse.Builder().code(429).addHeader("Retry-After", "30")
                .body("""{"error":{"code":"rate_limited","message":"too many requests"}}""").build()
        }

        model.requestConfirmation("alice@example.org", "password1")
        settle()

        val error = model.error
        assertTrue(error is AuthError.Api && error.error.code == "rate_limited")
        assertEquals(R.string.error_rate_limited_for, describe(error!!, registering = false).id)
        assertNull(model.notice)
    }

    /** Runs the view model's coroutines until the attempt has finished. */
    private fun settle() {
        val deadline = System.currentTimeMillis() + 5_000
        do {
            shadowOf(Looper.getMainLooper()).idle()
            Thread.sleep(10)
        } while (model.busy && System.currentTimeMillis() < deadline)
        shadowOf(Looper.getMainLooper()).idle()
        assertTrue("attempt finished", !model.busy)
    }

    private fun json(status: Int, body: String) = MockResponse.Builder().code(status).body(body).build()

    private fun tokenPair(): MockResponse {
        val now = Instant.now()
        return MockResponse.Builder()
            .code(200)
            .addHeader("Date", DateTimeFormatter.RFC_1123_DATE_TIME.format(now.atOffset(ZoneOffset.UTC)))
            .body(
                """{"user_id":"$USER","token_type":"Bearer","access_token":"access-1","refresh_token":"refresh-1",""" +
                    """"access_expires_at":"${now.plusSeconds(15 * 60)}","refresh_expires_at":"${now.plusSeconds(86_400)}"}""",
            )
            .build()
    }

    private fun error(status: Int, code: String, message: String) = json(status, """{"error":{"code":"$code","message":"$message"}}""")

    private companion object {
        const val USER = "00000000-0000-4000-8000-0000000000aa"
    }
}

private class MemoryStore : SessionStore {
    @Volatile
    var session: StoredSession? = null

    override suspend fun load(): StoredSession? = session

    override suspend fun save(session: StoredSession) {
        this.session = session
    }

    override suspend fun clear() {
        session = null
    }
}
