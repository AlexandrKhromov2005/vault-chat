package io.github.alexandrkhromov2005.vaultchat

import android.content.Context
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import io.github.alexandrkhromov2005.vaultchat.core.net.PushTokenRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.noContent
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionManager
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.tasks.await
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Push through FCM (docs/android/push.md, docs/push.md). A push is only a
 * hint to synchronise; the message itself comes through the API. The
 * Firebase project is set at build time outside the repository
 * (`vaultchat.firebase.*`); a build without it has no push and relies on
 * the periodic sync.
 */
class Push(
    private val context: Context,
    session: SessionManager,
    settings: Settings,
    private val scope: CoroutineScope,
) {
    val enabled: Boolean = BuildConfig.FIREBASE_APP_ID.isNotEmpty()

    private val tokens = PushTokens(
        session.state,
        settings,
        token = { FirebaseMessaging.getInstance().token.await() },
        delete = { FirebaseMessaging.getInstance().deleteToken().await() },
        send = { state, token ->
            session.authorized(state) { registerPushToken(it, PushTokenRequest(PROVIDER, token)).noContent() }
        },
    )

    /** Starts Firebase and registers the token of every login. */
    fun start() {
        if (!enabled) return
        val options = FirebaseOptions.Builder()
            .setApplicationId(BuildConfig.FIREBASE_APP_ID)
            .setApiKey(BuildConfig.FIREBASE_API_KEY)
            .setProjectId(BuildConfig.FIREBASE_PROJECT_ID)
            .setGcmSenderId(BuildConfig.FIREBASE_SENDER_ID)
            .build()
        if (FirebaseApp.getApps(context).isEmpty()) FirebaseApp.initializeApp(context, options)
        scope.launch { tokens.follow() }
    }

    /** Registers a new token FCM gave this installation. */
    fun onNewToken(token: String) {
        if (!enabled) return
        scope.launch { tokens.changed(token) }
    }

    private companion object {
        const val PROVIDER = "fcm"
    }
}

/**
 * Registers the installation's token for every login and deletes it when the
 * login ends. Logging out revokes the login on the server, which deletes its copy
 * of the token (docs/push.md). When the server cannot be reached, its copy stays
 * until the login expires; a token deleted at FCM wakes nothing any more, and FCM's
 * answer makes the server drop it. The next login gets a new token.
 */
internal class PushTokens(
    private val state: StateFlow<SessionState>,
    private val settings: Settings,
    private val token: suspend () -> String,
    private val delete: suspend () -> Unit,
    private val send: suspend (SessionState.SignedIn, String) -> Unit,
) {
    /**
     * Follows the session for the life of the process, one state after the other,
     * so that a login reads its token only after a delete under way has ended.
     */
    suspend fun follow() {
        state.collect { current ->
            when (current) {
                is SessionState.SignedIn -> register(current, token = null)
                is SessionState.SignedOut -> forget()
                SessionState.Restoring -> Unit
            }
        }
    }

    /** A new token from FCM, for the login signed in now. */
    suspend fun changed(token: String) {
        val current = state.value as? SessionState.SignedIn ?: return
        register(current, token)
    }

    // The token belongs to the login; a failure is tried again on the next
    // start or token change, and the periodic sync covers the gap.
    private suspend fun register(login: SessionState.SignedIn, token: String?) {
        try {
            withTimeoutOrNull(STEP_MS) {
                val current = token ?: this@PushTokens.token()
                settings.pushTokenBound = true
                send(login, current)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Without push the app still synchronises every 15 minutes.
        }
    }

    // Without a network this fails, and the next start tries again.
    private suspend fun forget() {
        if (!settings.pushTokenBound) return
        try {
            if (withTimeoutOrNull(STEP_MS) { delete() } != null) settings.pushTokenBound = false
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Kept for the next start; the server's own copy ends with the login.
        }
    }

    private companion object {
        // A hanging FCM call must not hold up the sessions after it for good.
        const val STEP_MS = 60_000L
    }
}

/** What a push means. */
object PushSignal {
    /**
     * Data only, {"type":"sync","room_id":...}; anything else is ignored. The
     * server sends it whatever this device's connection (docs/push.md), and a
     * live socket has the message already; a socket that breaks syncs when it
     * connects again.
     */
    fun wakesSync(data: Map<String, String>, socketLive: Boolean): Boolean = !socketLive && data["type"] == "sync"
}

/** Receives pushes and token changes from FCM. */
class PushService : FirebaseMessagingService() {
    override fun onMessageReceived(message: RemoteMessage) {
        val container = (application as VaultChatApplication).container
        if (PushSignal.wakesSync(message.data, container.chats.socketLive())) SyncWorker.runNow(applicationContext)
    }

    override fun onNewToken(token: String) {
        (application as VaultChatApplication).container.push.onNewToken(token)
    }
}
