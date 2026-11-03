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
import kotlinx.coroutines.launch
import kotlinx.coroutines.tasks.await

/**
 * Push through FCM (docs/android/push.md, docs/push.md). A push is only a
 * hint to synchronise; the message itself comes through the API. The
 * Firebase project is set at build time outside the repository
 * (`vaultchat.firebase.*`); a build without it has no push and relies on
 * the periodic sync.
 */
class Push(private val context: Context, private val session: SessionManager, private val scope: CoroutineScope) {
    val enabled: Boolean = BuildConfig.FIREBASE_APP_ID.isNotEmpty()

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
        scope.launch {
            session.state.collect { state ->
                if (state is SessionState.SignedIn) register(state, token = null)
            }
        }
    }

    /** Registers a new token FCM gave this installation. */
    fun onNewToken(token: String) {
        if (!enabled) return
        val state = session.state.value as? SessionState.SignedIn ?: return
        scope.launch { register(state, token) }
    }

    // The token belongs to the login; a failure is tried again on the next
    // start or token change, and the periodic sync covers the gap.
    private suspend fun register(state: SessionState.SignedIn, token: String?) {
        try {
            val current = token ?: FirebaseMessaging.getInstance().token.await()
            session.authorized(state) { registerPushToken(it, PushTokenRequest(PROVIDER, current)).noContent() }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Without push the app still synchronises every 15 minutes.
        }
    }

    private companion object {
        const val PROVIDER = "fcm"
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
