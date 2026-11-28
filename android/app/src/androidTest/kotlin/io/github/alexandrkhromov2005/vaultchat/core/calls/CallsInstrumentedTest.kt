package io.github.alexandrkhromov2005.vaultchat.core.calls

import android.Manifest
import android.util.Log
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
            gateway(),
            email,
            password,
        )
        File(ApplicationProvider.getApplicationContext<VaultChatApplication>().getExternalFilesDir(null), "calls-test-ready").writeText("ready")
    }

    @Test
    fun sessionProbe() = runBlocking {
        val container = ApplicationProvider.getApplicationContext<VaultChatApplication>().container
        val token = container.session.accessToken()
        val api = container.gateway.api(gateway())
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
        val api = container.gateway.api(gateway())
        await("call visible through authenticated REST", 180_000) {
            call { api.activeCalls(bearer(accessToken)) }.body.calls.isNotEmpty()
        }
        calls.reconcileNow()
        var lastReconcile = 0L
        await("incoming remote call", 60_000) {
            val now = System.currentTimeMillis()
            if (calls.view.value?.state != "ringing" && now - lastReconcile >= 1_000) {
                calls.reconcileNow()
                lastReconcile = now
            }
            calls.view.value?.state == "ringing"
        }
        calls.accept()
        awaitBidirectionalAudioAndEnd(calls)
    }

    private suspend fun awaitBidirectionalAudioAndEnd(calls: Calls) {
        var before = 0L to 0L
        var after = before
        var failure: Throwable? = null
        try {
            var lastDiagnostics = 0L
            var lastReconcile = 0L
            await("active remote audio", 900_000) {
                if (calls.view.value?.state == "ended") throw AssertionError("call ended before bidirectional audio")
                val now = System.currentTimeMillis()
                if (now - lastDiagnostics >= 15_000) {
                    Log.i("CallsAcceptance", "view=${calls.view.value}, ${calls.rtcDiagnostics()}")
                    lastDiagnostics = now
                }
                if (calls.view.value?.state == "ringing" && now - lastReconcile >= 1_000) {
                    calls.reconcileNow()
                    lastReconcile = now
                }
                calls.view.value?.state == "active" && calls.view.value?.remote == true
            }
            before = calls.audioBytes()
            after = before
            await("bidirectional Android audio", 30_000) {
                after = calls.audioBytes()
                after.first > before.first && after.second > before.second
            }
            assertTrue("Android sent no audio: $before -> $after", after.first > before.first)
            assertTrue("Android received no audio: $before -> $after", after.second > before.second)
            if (androidx.test.platform.app.InstrumentationRegistry.getArguments().getString("coordinate") == "true") {
                if (calls.view.value?.role == CallRole.CALLEE) {
                    testFile("calls-test-callee-rtp").writeText("passed")
                    await("caller ended", 120_000) { calls.view.value?.state == "ended" }
                } else {
                    await("callee bidirectional audio", 900_000) { testFile("calls-test-caller-release").exists() }
                }
            }
        } catch (caught: Throwable) {
            failure = caught
            val diagnostics = runCatching { calls.rtcDiagnostics() }.getOrElse { "diagnostics failed: $it" }
            throw AssertionError("view=${calls.view.value}, audio=$before -> $after, $diagnostics", caught)
        } finally {
            if (failure == null) {
                calls.end()
                await("ended", 15_000) { calls.view.value?.state == "ended" }
            } else {
                runCatching { calls.end() }
            }
        }
    }

    private fun gateway(): ServerAddress {
        val configured = androidx.test.platform.app.InstrumentationRegistry.getArguments().getString("gateway")
        return ServerAddress.parse(configured ?: "http://10.0.2.2:8080", allowLocalCleartext = true)
    }

    private fun testFile(name: String): File =
        File(ApplicationProvider.getApplicationContext<VaultChatApplication>().getExternalFilesDir(null), name)

    private suspend fun await(description: String, timeoutMs: Long, condition: suspend () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (condition()) return
            delay(100)
        }
        throw AssertionError("Timed out: $description")
    }
}
