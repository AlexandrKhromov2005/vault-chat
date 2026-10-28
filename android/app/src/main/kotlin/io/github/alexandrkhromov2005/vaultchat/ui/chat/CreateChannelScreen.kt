package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChannelName
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSession
import io.github.alexandrkhromov2005.vaultchat.core.chat.Membership
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.ui.auth.Message
import io.github.alexandrkhromov2005.vaultchat.ui.auth.text
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class CreateChannelViewModel(private val chat: ChatSession) : ViewModel() {
    init {
        addCloseable(chat.retain())
    }

    private val mutableCreating = MutableStateFlow(false)
    val creating: StateFlow<Boolean> = mutableCreating.asStateFlow()

    private val mutableError = MutableStateFlow<Message?>(null)
    internal val error: StateFlow<Message?> = mutableError.asStateFlow()

    /**
     * Creates the channel once. The request is never repeated by itself: if
     * its outcome is unknown, the user is asked to look at the room list
     * first. It finishes even if the screen closes meanwhile.
     */
    fun create(name: String, private: Boolean, onCreated: (String) -> Unit) {
        val normalized = ChannelName.normalize(name)
        if (mutableCreating.value || ChannelName.check(normalized) != null) return
        mutableCreating.value = true
        mutableError.value = null
        viewModelScope.launch {
            val result = withContext(NonCancellable) {
                try {
                    Result.success(chat.membership.createChannel(normalized, private))
                } catch (e: ApiException) {
                    Result.failure(e)
                } catch (e: Exception) {
                    // Unexpected here; whether the channel was created is unknown.
                    Result.failure(ApiException(0, ApiException.LOCAL, "local failure", cause = e))
                }
            }
            mutableCreating.value = false
            result.fold(onCreated) { e ->
                val failure = e as ApiException
                mutableError.value = if (Membership.isUnknownOutcome(failure)) {
                    Message(R.string.channel_unknown)
                } else {
                    describe(failure, ChatAction.CREATE_CHANNEL)
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CreateChannelScreen(chat: ChatSession, onBack: () -> Unit, onCreated: (String) -> Unit) {
    val model = viewModel { CreateChannelViewModel(chat) }
    val creating by model.creating.collectAsStateWithLifecycle()
    val error by model.error.collectAsStateWithLifecycle()
    var name by rememberSaveable { mutableStateOf("") }
    var private by rememberSaveable { mutableStateOf(false) }
    val normalized = ChannelName.normalize(name)
    val problem = ChannelName.check(normalized)
    val length = normalized.codePointCount(0, normalized.length)
    val submit = { model.create(name, private, onCreated) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.back))
                    }
                },
                title = { Text(stringResource(R.string.new_channel)) },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).imePadding().verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            OutlinedTextField(
                value = name,
                onValueChange = { name = it },
                label = { Text(stringResource(R.string.channel_name)) },
                singleLine = true,
                isError = problem == ChannelName.Problem.INVALID_CHARACTER || problem == ChannelName.Problem.TOO_LONG,
                supportingText = {
                    Text(
                        if (problem == ChannelName.Problem.INVALID_CHARACTER) {
                            stringResource(R.string.channel_name_invalid)
                        } else {
                            stringResource(R.string.channel_name_length, length, ChannelName.MAX_CODE_POINTS)
                        },
                    )
                },
                keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences, imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { submit() }),
                enabled = !creating,
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
            )
            Row(
                Modifier.fillMaxWidth().toggleable(value = private, enabled = !creating, role = Role.Switch) { private = it },
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                Column(Modifier.weight(1f)) {
                    Text(stringResource(R.string.channel_private), style = MaterialTheme.typography.titleMedium)
                    Text(
                        stringResource(R.string.channel_private_hint),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                // The row handles the toggle, so that the whole row is the target.
                Switch(checked = private, onCheckedChange = null, enabled = !creating)
            }
            error?.let {
                Text(
                    it.text(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                )
            }
            Button(onClick = submit, enabled = problem == null && !creating, modifier = Modifier.align(Alignment.End)) {
                if (creating) {
                    CircularProgressIndicator(Modifier.padding(end = 8.dp).size(18.dp), strokeWidth = 2.dp)
                }
                Text(stringResource(R.string.channel_create))
            }
        }
    }
}
