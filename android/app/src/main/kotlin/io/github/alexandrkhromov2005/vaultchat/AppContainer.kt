package io.github.alexandrkhromov2005.vaultchat

import android.content.Context
import android.os.SystemClock
import androidx.core.content.edit
import io.github.alexandrkhromov2005.vaultchat.core.net.Gateway
import io.github.alexandrkhromov2005.vaultchat.core.session.EncryptedFileSessionStore
import io.github.alexandrkhromov2005.vaultchat.core.session.KeystoreSessionCipher
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionManager
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import okhttp3.OkHttpClient
import java.io.File
import java.util.concurrent.TimeUnit

/** The app's long-lived objects, built once per process. */
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
