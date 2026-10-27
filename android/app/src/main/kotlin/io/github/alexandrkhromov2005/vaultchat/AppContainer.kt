package io.github.alexandrkhromov2005.vaultchat

import android.content.Context
import android.os.SystemClock
import androidx.core.content.edit
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSessionDeps
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSessions
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabases
import io.github.alexandrkhromov2005.vaultchat.core.net.Gateway
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import io.github.alexandrkhromov2005.vaultchat.core.session.EncryptedFileSessionStore
import io.github.alexandrkhromov2005.vaultchat.core.session.KeystoreSessionCipher
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionManager
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import io.github.alexandrkhromov2005.vaultchat.core.system.AppForeground
import io.github.alexandrkhromov2005.vaultchat.core.system.NetworkMonitor
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.StateFlow
import okhttp3.OkHttpClient
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import retrofit2.Response
import java.io.File
import java.util.concurrent.TimeUnit

/** The app's long-lived objects, built once per process on the main thread. */
class AppContainer(context: Context) {

    /** Work that must outlive a screen, such as ending a session on the server. */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    // Gateway answers within its 10 s request budget; the margin covers a
    // slow mobile network.
    private val http = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .writeTimeout(20, TimeUnit.SECONDS)
        .callTimeout(30, TimeUnit.SECONDS)
        .build()

    val allowLocalCleartext: Boolean = BuildConfig.DEBUG

    val gateway = Gateway(http)

    // noBackupFilesDir is never included in backups.
    val session = SessionManager(
        store = EncryptedFileSessionStore(File(context.noBackupFilesDir, "session.bin"), KeystoreSessionCipher()),
        gateway = gateway,
        allowLocalCleartext = allowLocalCleartext,
        now = System::currentTimeMillis,
        // Runs in deep sleep too, unlike System.nanoTime on Android.
        elapsed = SystemClock::elapsedRealtime,
    )

    val settings = Settings(context)

    private val foreground = AppForeground()
    private val networkMonitor = NetworkMonitor(context)

    val chats = ChatSessions(ChatDatabases(context), object : ChatSessionDeps {
        override val foreground: StateFlow<Boolean> = this@AppContainer.foreground.visible
        override val network: StateFlow<Any?> = networkMonitor.network
        override val elapsed: () -> Long = SystemClock::elapsedRealtime

        override suspend fun <T : Any> send(session: SessionState.SignedIn, request: suspend GatewayApi.(String) -> Response<T>): T =
            this@AppContainer.session.authorized(session, request).body

        override suspend fun grant(session: SessionState.SignedIn, minValidityMs: Long): AccessGrant =
            this@AppContainer.session.accessGrant(session, minValidityMs)

        override suspend fun renew(session: SessionState.SignedIn, rejected: String): AccessGrant =
            this@AppContainer.session.renewAccess(session, rejected)

        override fun openSocket(session: SessionState.SignedIn, listener: WebSocketListener): WebSocket =
            gateway.webSocket(session.server, listener)
    }, appScope).also { it.follow(session.state) }
}

/** Preferences that are not secret. */
class Settings(context: Context) {
    private val preferences = context.getSharedPreferences("settings", Context.MODE_PRIVATE)

    var lastServer: String?
        get() = preferences.getString(KEY_LAST_SERVER, null)
        set(value) = preferences.edit { putString(KEY_LAST_SERVER, value) }

    private companion object {
        const val KEY_LAST_SERVER = "last_server"
    }
}
