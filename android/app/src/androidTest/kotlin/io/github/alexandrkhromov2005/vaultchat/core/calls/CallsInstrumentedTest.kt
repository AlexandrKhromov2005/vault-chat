package io.github.alexandrkhromov2005.vaultchat.core.calls

import android.Manifest
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.rule.GrantPermissionRule
import io.github.alexandrkhromov2005.vaultchat.VaultChatApplication
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.net.bearer
import io.github.alexandrkhromov2005.vaultchat.core.net.call
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

@RunWith(AndroidJUnit4::class)
class CallsInstrumentedTest {
    @get:Rule
    val microphone: GrantPermissionRule = GrantPermissionRule.grant(Manifest.permission.RECORD_AUDIO)

    @Before
    fun login() = runBlocking {
        val arguments = androidx.test.platform.app.InstrumentationRegistry.getArguments()
        val email = arguments.getString("email") ?: error("email is required")
        val password = arguments.getString("password") ?: error("password is required")
        val container = ApplicationProvider.getApplicationContext<VaultChatApplication>().container
        container.session.login(
            ServerAddress.parse("http://10.0.2.2:8080", allowLocalCleartext = true),
            email,
            password,
        )
        File(ApplicationProvider.getApplicationContext<VaultChatApplication>().getExternalFilesDir(null), "calls-test-ready").writeText("ready")
    }

    @Test
    fun sessionProbe() = runBlocking {
        val container = ApplicationProvider.getApplicationContext<VaultChatApplication>().container
        val token = container.session.accessToken()
        val api = container.gateway.api(ServerAddress.parse("http://10.0.2.2:8080", allowLocalCleartext = true))
        val direct = call { api.me(bearer(token)) }.body
        val authorized = try {
            container.session.authorized { me(it) }.body
        } catch (e: Exception) {
            throw IllegalStateException("authorized call failed after direct succeeded", e)
        }
        assertTrue(direct.userId == authorized.userId)
    }

    @Test
    fun startsAndroidAudioAndReleasesMicrophone() = runBlocking {
        val roomId = androidx.test.platform.app.InstrumentationRegistry.getArguments().getString("roomId")
            ?: error("roomId is required")
        val calls = ApplicationProvider.getApplicationContext<VaultChatApplication>().container.chats.awaitCurrent()!!.calls
        calls.start(roomId)
        awaitBidirectionalAudioAndEnd(calls)
    }

    @Test
    fun acceptsBrowserAudioAndReleasesMicrophone() = runBlocking {
        val container = ApplicationProvider.getApplicationContext<VaultChatApplication>().container
        val calls = container.chats.awaitCurrent()!!.calls
        val signedIn = container.session.state.value as io.github.alexandrkhromov2005.vaultchat.core.session.SessionState.SignedIn
        val me = container.session.authorized(signedIn) { me(it) }.body
        assertTrue("authorized path returned another user", me.userId == signedIn.userId)
        val accessToken = container.session.accessToken()
        assertTrue("session issued an empty access token", accessToken.isNotBlank())
        val server = ServerAddress.parse("http://10.0.2.2:8080", allowLocalCleartext = true)
        val api = container.gateway.api(server)
        await("call visible through authenticated REST", 180_000) {
            call { api.activeCalls(bearer(accessToken)) }.body.calls.isNotEmpty()
        }
        calls.reconcileNow()
        await("incoming browser call", 60_000) {
            if (calls.view.value?.state != "ringing") calls.reconcileNow()
            calls.view.value?.state == "ringing"
        }
        calls.accept()
        awaitBidirectionalAudioAndEnd(calls)
    }

    private suspend fun awaitBidirectionalAudioAndEnd(calls: Calls) {
        await("active remote audio", 900_000) {
            if (calls.view.value?.state != "active") calls.reconcileNow()
            calls.view.value?.state == "active" && calls.view.value?.remote == true
        }
        val before = calls.audioBytes()
        var after = before
        try {
            await("bidirectional Android audio", 30_000) {
                after = calls.audioBytes()
                after.first > before.first && after.second > before.second
            }
        } finally {
            calls.end()
            await("ended", 15_000) { calls.view.value?.state == "ended" }
        }
        assertTrue("Android sent no audio: $before -> $after", after.first > before.first)
        assertTrue("Android received no audio: $before -> $after", after.second > before.second)
    }

    private suspend fun await(description: String, timeoutMs: Long, condition: suspend () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (condition()) return
            delay(100)
        }
        throw AssertionError("Timed out: $description")
    }
}
