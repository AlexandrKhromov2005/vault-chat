package io.github.alexandrkhromov2005.vaultchat

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.github.alexandrkhromov2005.vaultchat.core.chat.isUuid
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import io.github.alexandrkhromov2005.vaultchat.ui.SessionScope
import io.github.alexandrkhromov2005.vaultchat.ui.auth.AuthFlow
import io.github.alexandrkhromov2005.vaultchat.ui.chat.ChatHome
import io.github.alexandrkhromov2005.vaultchat.ui.theme.VaultChatTheme

class MainActivity : ComponentActivity() {
    // A tapped notification while the app is open.
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        openRoomOf(intent)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val container = (application as VaultChatApplication).container
        // Not again when the activity is recreated with the same intent.
        if (savedInstanceState == null) openRoomOf(intent)
        setContent {
            VaultChatTheme {
                VaultChatRoot(container)
            }
        }
    }
}

private fun ComponentActivity.openRoomOf(intent: Intent?) {
    val roomId = intent?.getStringExtra(Notifications.EXTRA_ROOM)?.takeIf(::isUuid) ?: return
    (application as VaultChatApplication).container.openRoom.value = roomId
}

@Composable
private fun VaultChatRoot(container: AppContainer) {
    val state by container.session.state.collectAsStateWithLifecycle()
    // Each session state gets its own view models; see SessionScope.
    SessionScope(key = state) {
        when (val current = state) {
            SessionState.Restoring -> Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background))
            is SessionState.SignedOut -> AuthFlow(container, current.reason)
            is SessionState.SignedIn -> ChatHome(container, current)
        }
    }
}
