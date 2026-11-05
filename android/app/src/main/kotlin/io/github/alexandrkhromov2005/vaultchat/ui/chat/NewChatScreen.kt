package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSession
import io.github.alexandrkhromov2005.vaultchat.core.chat.UserSearch
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.PublicProfile
import io.github.alexandrkhromov2005.vaultchat.ui.auth.text
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class NewChatViewModel(private val chat: ChatSession) : ViewModel() {
    init {
        addCloseable(chat.retain())
    }

    val search = UserSearch(viewModelScope, chat.membership::search)

    private val mutableOpening = MutableStateFlow<String?>(null)

    /** The user whose dialog is being opened. */
    val opening: StateFlow<String?> = mutableOpening.asStateFlow()

    private val mutableError = MutableStateFlow<ApiException?>(null)
    val error: StateFlow<ApiException?> = mutableError.asStateFlow()

    /** Opens the dialog with [user], one at a time, and passes its room to [onOpened]. */
    fun open(user: PublicProfile, onOpened: (String) -> Unit) {
        if (mutableOpening.value != null) return
        mutableOpening.value = user.userId
        mutableError.value = null
        viewModelScope.launch {
            try {
                onOpened(chat.membership.openDialog(user.userId))
            } catch (e: ApiException) {
                mutableError.value = e
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                mutableError.value = ApiException(0, ApiException.LOCAL, "local failure", cause = e)
            } finally {
                mutableOpening.value = null
            }
        }
    }
}

/** Search for a user to write to, with "create channel" above the results. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewChatScreen(chat: ChatSession, onBack: () -> Unit, onOpened: (String) -> Unit, onCreateChannel: () -> Unit) {
    val model = viewModel { NewChatViewModel(chat) }
    val state by model.search.state.collectAsStateWithLifecycle()
    val opening by model.opening.collectAsStateWithLifecycle()
    val error by model.error.collectAsStateWithLifecycle()
    var text by rememberSaveable { mutableStateOf("") }
    // A screen restored after the process was killed searches again for what it showed.
    LaunchedEffect(Unit) { model.search.onInput(text) }
    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.back))
                    }
                },
                title = {
                    SearchField(text) {
                        text = it
                        model.search.onInput(it)
                    }
                },
            )
        },
    ) { padding ->
        LazyColumn(Modifier.fillMaxSize().padding(padding).imePadding()) {
            item(key = "create") {
                ActionRow(R.drawable.ic_group, stringResource(R.string.create_channel), onCreateChannel)
                HorizontalDivider()
            }
            error?.let { failure ->
                item(key = "open-error") {
                    Text(
                        describe(failure, ChatAction.OPEN_DIALOG).text(),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp)
                            .semantics { liveRegion = LiveRegionMode.Polite },
                    )
                }
            }
            userResults(state, busy = opening, onPick = { model.open(it, onOpened) }, onRetry = model.search::retry)
        }
    }
}
