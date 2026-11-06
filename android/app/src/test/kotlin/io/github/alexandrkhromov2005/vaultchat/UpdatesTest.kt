package io.github.alexandrkhromov2005.vaultchat

import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
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
import java.util.concurrent.TimeUnit

@RunWith(RobolectricTestRunner::class)
class UpdatesTest {
    private val server = MockWebServer()
    private val settings = Settings(ApplicationProvider.getApplicationContext())
    private var now = 1_000_000_000L
    private val day = 25 * 60 * 60 * 1000L
    private val pages = "https://github.com/AlexandrKhromov2005/vault-chat/releases/"

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

    private fun release(tag: String, page: String = "${pages}tag/$tag", prerelease: Boolean = false, draft: Boolean = false, padding: Int = 0) =
        """{"tag_name":"$tag","html_url":"$page","draft":$draft,"prerelease":$prerelease${if (padding > 0) ""","pad":"${"x".repeat(padding)}"""" else ""}}"""

    private fun answer(vararg releases: String) {
        server.enqueue(MockResponse.Builder().body("[${releases.joinToString(",")}]").build())
    }

    /** A valid list offering 0.9.0, padded to exactly [bytes]. */
    private fun answerOfSize(bytes: Int) {
        val brackets = 2
        val padding = bytes - brackets - release("android-v0.9.0", padding = 1).length + 1
        answer(release("android-v0.9.0", padding = padding))
    }

    private fun offer(version: String) = AppUpdate(version, "${pages}tag/android-v$version")

    /** A saved offer of 0.2.0, and a day has passed. */
    private suspend fun offered(): Updates {
        answer(release("android-v0.2.0"))
        val updates = updates()
        updates.checkIfDue()
        assertEquals(offer("0.2.0"), updates.update.value)
        now += day
        return updates
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
        assertEquals(offer("0.2.0"), updates.update.value)
        // Remembered until the next check.
        assertEquals(updates.update.value, updates().update.value)
    }

    @Test
    fun draftsAndPrereleasesAreSkipped() = runBlocking {
        answer(
            release("android-v0.9.0", draft = true),
            release("android-v0.8.0", prerelease = true),
            release("android-v0.2.0"),
        )
        val updates = updates()
        updates.checkIfDue()
        assertEquals(offer("0.2.0"), updates.update.value)
    }

    @Test
    fun onlyThePageOfTheReleaseItselfIsOffered() = runBlocking {
        answer(
            release("android-v0.5.0", page = "${pages}../../../evil/repo/releases/tag/x"),
            release("android-v0.4.0", page = "${pages}tag/android-v0.9.9"),
            release("android-v0.3.0", page = "${pages}tag/android-v0.3.0/../../../../evil/repo"),
        )
        val updates = updates()
        updates.checkIfDue()
        assertNull("a page outside the release was offered: ${updates.update.value}", updates.update.value)
    }

    @Test
    fun githubIsAskedAtMostOnceADayAndOnlyNewerVersionsCount() = runBlocking {
        answer(release("android-v0.1.0"))
        val updates = updates()
        updates.checkIfDue()
        assertNull(updates.update.value)
        updates.checkIfDue()
        assertEquals(1, server.requestCount)

        now += day
        answer(release("android-v0.2.0"))
        updates.checkIfDue()
        assertEquals(2, server.requestCount)
        assertEquals(offer("0.2.0"), updates.update.value)
    }

    @Test
    fun aRefusalKeepsTheOffer() = runBlocking {
        val updates = offered()

        server.enqueue(MockResponse.Builder().code(403).build())
        updates.checkIfDue()

        assertEquals(2, server.requestCount)
        assertEquals("a 403 hid the offer", offer("0.2.0"), updates.update.value)
        assertEquals("the saved offer was lost", offer("0.2.0"), updates().update.value)
    }

    @Test
    fun anAnswerThatIsNotTheListKeepsTheOffer() = runBlocking {
        val updates = offered()

        server.enqueue(MockResponse.Builder().body("<html>not json</html>").build())
        updates.checkIfDue()

        assertEquals(offer("0.2.0"), updates.update.value)
    }

    @Test
    fun anAnswerOverTheLimitIsNotUsedAndKeepsTheOffer() = runBlocking {
        val updates = offered()

        answerOfSize(1024 * 1024 + 1)
        updates.checkIfDue()

        assertEquals("an answer over 1 MiB was used", offer("0.2.0"), updates.update.value)
    }

    @Test
    fun anAnswerUpToTheLimitIsUsed() = runBlocking {
        answerOfSize(1024 * 1024)
        val updates = updates()
        updates.checkIfDue()

        assertEquals(offer("0.9.0"), updates.update.value)
    }

    @Test
    fun anAnswerThatSaysThereIsNothingNewerEndsTheOffer() = runBlocking {
        val updates = offered()

        answer(release("android-v0.1.0"))
        updates.checkIfDue()

        assertNull(updates.update.value)
        assertNull(updates().update.value)
    }

    @Test
    fun aServerFailureWaitsForTheNextDay() = runBlocking {
        server.enqueue(MockResponse.Builder().code(500).build())
        val updates = updates()
        updates.checkIfDue()
        updates.checkIfDue()
        assertEquals(1, server.requestCount)

        now += day
        answer(release("android-v0.2.0"))
        updates.checkIfDue()
        assertEquals(offer("0.2.0"), updates.update.value)
    }

    @Test
    fun aCheckThatGotNoAnswerTriesAgainWithoutWaitingADay() = runBlocking {
        val offline = Updates(OkHttpClient(), settings, "0.1.0", { now }, "http://127.0.0.1:1/releases")
        offline.checkIfDue()

        answer(release("android-v0.2.0"))
        val updates = updates()
        updates.checkIfDue()

        assertEquals("the failed connection cost the day", offer("0.2.0"), updates.update.value)
    }

    @Test
    fun aCancelledCheckDoesNotCostTheDay() = runBlocking {
        server.enqueue(MockResponse.Builder().headersDelay(1, TimeUnit.SECONDS).body("[]").build())
        val updates = updates()
        assertNull(withTimeoutOrNull(100) { updates.checkIfDue() })

        answer(release("android-v0.2.0"))
        updates.checkIfDue()

        assertEquals("the cancelled check cost the day", offer("0.2.0"), updates.update.value)
    }

    @Test
    fun aClockThatWasAheadDoesNotBlockChecks() = runBlocking {
        settings.updateCheckedAt = now + 365 * day
        answer(release("android-v0.2.0"))
        val updates = updates()
        updates.checkIfDue()

        assertEquals("a check from the future blocked this one", offer("0.2.0"), updates.update.value)
    }

    @Test
    fun versionsAreComparedNumberByNumber() {
        assertTrue(Updates.newer("0.10.0", "0.9.9"))
        assertTrue(Updates.newer("1.0", "0.99.99"))
        assertFalse(Updates.newer("0.1.0", "0.1"))
        assertFalse(Updates.newer("0.1.0-rc", "0.0.1"))
    }
}
