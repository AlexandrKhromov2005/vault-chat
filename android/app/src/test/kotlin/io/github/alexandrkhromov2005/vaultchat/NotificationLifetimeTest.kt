package io.github.alexandrkhromov2005.vaultchat

import android.app.Application
import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.RoomNotice
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.NoticeItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionStore
import io.github.alexandrkhromov2005.vaultchat.core.session.StoredSession
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Nothing of an account stays visible or actionable after sign-out or
 * revocation. Through the real container, so that its wiring is what is tested;
 * the plain application keeps a second container from cleaning up as well.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class)
class NotificationLifetimeTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val manager = shadowOf(context.getSystemService(NotificationManager::class.java))
    private val roomId = "00000000-0000-4000-8000-000000000001"
    private val server = MockWebServer()
    private var container: AppContainer? = null

    @After
    fun tearDown() {
        container?.appScope?.cancel()
        server.close()
    }

    private class MemoryStore(var session: StoredSession?) : SessionStore {
        override suspend fun load(): StoredSession? = session

        override suspend fun save(session: StoredSession) {
            this.session = session
        }

        override suspend fun clear() {
            session = null
        }
    }

    /** A container with a stored session of a synthetic user on this test's server. */
    private suspend fun signedIn(): AppContainer {
        server.start()
        val stored = StoredSession(server.url("/").toString().trimEnd('/'), "00000000-0000-4000-8000-0000000000aa", "alice", "synthetic-refresh")
        val created = AppContainer(context, MemoryStore(stored)).also { container = it }
        created.session.restore()
        assertTrue(created.session.state.value is SessionState.SignedIn)
        Notifications.createChannels(context)
        return created
    }

    private fun notice(room: String = roomId) = RoomNotice(
        room,
        listOf(
            NoticeItem(
                room, 1, "00000000-0000-4000-8000-0000000000bb", "bob", senderKnown = true, "synthetic text", attachments = "",
                createdAt = 1_000_000, roomKind = RoomEntity.DIRECT, roomName = null, peerName = "bob",
            ),
        ),
    )

    private suspend fun waitFor(what: String, condition: () -> Boolean) {
        repeat(150) {
            if (condition()) return
            delay(20)
        }
        assertTrue(what, condition())
    }

    @Test
    fun signingOutRemovesTheNotificationsAndThePendingRoom() = runBlocking {
        val app = signedIn()
        Notifications.post(context, "account", listOf(notice()))
        assertEquals(1, manager.activeNotifications.size)
        app.openRoom.tap("account", roomId)

        server.enqueue(MockResponse.Builder().code(204).build())
        app.session.logout()

        waitFor("text of the signed-out account is still in the shade") { manager.activeNotifications.isEmpty() }
        waitFor("a room of the signed-out account is still pending") { app.openRoom.pending.value == null }
    }

    @Test
    fun aRevokedSessionRemovesTheNotifications() = runBlocking {
        val app = signedIn()
        Notifications.post(context, "account", listOf(notice()))

        // Revoke-all from the web: the next refresh is refused.
        repeat(2) { server.enqueue(MockResponse.Builder().code(401).body("""{"error":{"code":"unauthorized","message":"no"}}""").build()) }
        runCatching { app.session.accessToken() }

        assertEquals(SessionState.SignedOut(SessionState.Reason.EXPIRED), app.session.state.value)
        waitFor("text of the revoked session is still in the shade") { manager.activeNotifications.isEmpty() }
    }

    @Test
    fun aTapCarriesTheAccountTheNoticeWasPostedFor() = runBlocking {
        Notifications.createChannels(context)
        Notifications.post(context, "account-a", listOf(notice()))

        val intent = shadowOf(manager.activeNotifications.single().notification.contentIntent).savedIntent

        assertEquals("account-a", intent.getStringExtra(Notifications.EXTRA_ACCOUNT))
        assertEquals(roomId, intent.getStringExtra(Notifications.EXTRA_ROOM))
    }

    @Test
    fun theChatOfAnotherAccountDropsAPendingRoom() {
        val rooms = PendingRooms(MutableStateFlow(SessionState.Restoring))
        rooms.tap("account-a", roomId)

        assertNull("account B got a room of account A", rooms.take("account-b"))
        assertNull(rooms.pending.value)
    }

    @Test
    fun theChatOfTheAccountTakesItsPendingRoomOnce() {
        val rooms = PendingRooms(MutableStateFlow(SessionState.Restoring))
        rooms.tap("account-a", roomId)

        assertEquals(roomId, rooms.take("account-a"))
        assertNull(rooms.take("account-a"))
    }

    @Test
    fun aTapWhileSignedOutOpensNothingLater() {
        val rooms = PendingRooms(MutableStateFlow(SessionState.SignedOut(SessionState.Reason.LOGOUT)))

        rooms.tap("account-a", roomId)

        assertNull("a stale notification left a room pending", rooms.pending.value)
    }

    @Test
    fun anIntentOfAnotherAppOpensNothing() {
        val rooms = PendingRooms(MutableStateFlow(SessionState.Restoring))

        rooms.tap("account-a", "../../not-a-uuid")
        assertNull("a value that is no room id was kept", rooms.pending.value)
        rooms.tap(null, roomId)
        assertNull("a tap for no account was kept", rooms.pending.value)
    }
}
