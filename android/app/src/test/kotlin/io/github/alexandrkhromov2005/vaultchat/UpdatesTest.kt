package io.github.alexandrkhromov2005.vaultchat

import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class UpdatesTest {
    private val server = MockWebServer()
    private val settings = Settings(ApplicationProvider.getApplicationContext())
    private var now = 1_000_000_000L

    @Before
    fun setUp() {
        server.start()
    }

    @After
    fun tearDown() {
        server.close()
    }

    private fun updates(current: String = "0.1.0") =
        Updates(OkHttpClient(), settings, current, { now }, server.url("/releases").toString())

    private fun release(tag: String, page: String = "https://github.com/AlexandrKhromov2005/vault-chat/releases/tag/$tag", prerelease: Boolean = false) =
        """{"tag_name":"$tag","html_url":"$page","draft":false,"prerelease":$prerelease}"""

    private fun answer(vararg releases: String) {
        server.enqueue(MockResponse.Builder().body("[${releases.joinToString(",")}]").build())
    }

    @Test
    fun theNewestReleaseOfTheAppIsOffered() = runBlocking {
        answer(
            release("v2.0.0"),
            release("android-v0.3.0-beta", prerelease = true),
            release("android-v0.10.0", page = "https://evil.example/releases/x"),
            release("android-v0.2.0"),
            release("android-v0.1.5"),
        )
        val updates = updates()
        updates.checkIfDue()
        // Not the backend's, not a prerelease, not a page elsewhere; 0.2.0 > 0.1.5.
        assertEquals(AppUpdate("0.2.0", "https://github.com/AlexandrKhromov2005/vault-chat/releases/tag/android-v0.2.0"), updates.update.value)
        // Remembered until the next check.
        assertEquals(updates.update.value, updates().update.value)
    }

    @Test
    fun githubIsAskedAtMostOnceADayAndOnlyNewerVersionsCount() = runBlocking {
        answer(release("android-v0.1.0"))
        val updates = updates()
        updates.checkIfDue()
        assertNull(updates.update.value)
        updates.checkIfDue()
        assertEquals(1, server.requestCount)

        now += 25 * 60 * 60 * 1000L
        server.enqueue(MockResponse.Builder().code(500).build())
        updates.checkIfDue()
        assertNull(updates.update.value)
        assertEquals(2, server.requestCount)
    }

    @Test
    fun versionsAreComparedNumberByNumber() {
        assertTrue(Updates.newer("0.10.0", "0.9.9"))
        assertTrue(Updates.newer("1.0", "0.99.99"))
        assertFalse(Updates.newer("0.1.0", "0.1"))
        assertFalse(Updates.newer("0.1.0-rc", "0.0.1"))
    }
}
