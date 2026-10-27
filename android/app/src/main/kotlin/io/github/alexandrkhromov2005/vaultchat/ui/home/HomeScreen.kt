package io.github.alexandrkhromov2005.vaultchat.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import io.github.alexandrkhromov2005.vaultchat.AppContainer
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionManager
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/** Whether the signed-in session is accepted by the server right now. */
sealed interface Connection {
    data object Checking : Connection

    data object Online : Connection

    data class Failed(val error: ApiException) : Connection
}

class HomeViewModel(private val session: SessionManager, private val appScope: CoroutineScope) : ViewModel() {
    var connection by mutableStateOf<Connection>(Connection.Checking)
        private set

    init {
        check()
    }

    fun check() {
        connection = Connection.Checking
        viewModelScope.launch {
            connection = try {
                session.authorized { me(it) }
                Connection.Online
            } catch (e: ApiException) {
                Connection.Failed(e)
            }
        }
    }

    // Ending the session on the server must not stop when this screen goes away.
    fun logout() {
        appScope.launch { session.logout() }
    }
}

/** A placeholder until rooms arrive: who is signed in, and the connection. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(container: AppContainer, signedIn: SessionState.SignedIn) {
    val model = viewModel { HomeViewModel(container.session, container.appScope) }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.app_name)) },
                actions = {
                    TextButton(onClick = model::logout) { Text(stringResource(R.string.logout)) }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).padding(horizontal = 24.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(signedIn.username, style = MaterialTheme.typography.titleLarge)
            Text(
                signedIn.server.toString(),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            ConnectionStatus(model.connection, onRetry = model::check)
            Text(
                stringResource(R.string.home_placeholder),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ConnectionStatus(connection: Connection, onRetry: () -> Unit) {
    val text = when (connection) {
        Connection.Checking -> stringResource(R.string.connection_checking)
        Connection.Online -> stringResource(R.string.connection_online)
        is Connection.Failed -> stringResource(
            if (connection.error.code == ApiException.NETWORK) R.string.connection_offline else R.string.connection_failed,
        )
    }
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        color = if (connection is Connection.Failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
    if (connection is Connection.Failed) {
        TextButton(onClick = onRetry) { Text(stringResource(R.string.retry)) }
    }
}
