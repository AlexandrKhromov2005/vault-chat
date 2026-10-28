package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.ExperimentalMaterial3Api
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
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class AddMemberViewModel(private val chat: ChatSession, private val roomId: String) : ViewModel() {
    init {
        addCloseable(chat.retain())
    }

    val search = UserSearch(viewModelScope, chat.membership::search)

    /** Users already in the stored roster, shown but not offered. */
    val members: StateFlow<Set<String>> = chat.membership.members(roomId).map { list -> list.mapTo(HashSet()) { it.userId } }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), emptySet())

    val room: StateFlow<Boolean> = chat.room(roomId).map { it != null }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), true)

    private val mutableAdding = MutableStateFlow<String?>(null)
    val adding: StateFlow<String?> = mutableAdding.asStateFlow()

    private val mutableError = MutableStateFlow<ApiException?>(null)
    val error: StateFlow<ApiException?> = mutableError.asStateFlow()

    /** Adds [user] once; it finishes even if the screen closes meanwhile. */
    fun add(user: PublicProfile, onAdded: () -> Unit) {
        if (mutableAdding.value != null) return
        mutableAdding.value = user.userId
        mutableError.value = null
        viewModelScope.launch {
            val failure = withContext(NonCancellable) {
                try {
                    chat.membership.addMember(roomId, user.userId)
                    null
                } catch (e: ApiException) {
                    e
                }
            }
            mutableAdding.value = null
            if (failure == null) onAdded() else mutableError.value = failure
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddMemberScreen(chat: ChatSession, roomId: String, onBack: () -> Unit, onGone: () -> Unit) {
    val model = viewModel { AddMemberViewModel(chat, roomId) }
    val state by model.search.state.collectAsStateWithLifecycle()
    val members by model.members.collectAsStateWithLifecycle()
    val adding by model.adding.collectAsStateWithLifecycle()
    val error by model.error.collectAsStateWithLifecycle()
    val present by model.room.collectAsStateWithLifecycle()
    var text by rememberSaveable { mutableStateOf("") }
    val alreadyMember = stringResource(R.string.members_already)
    LaunchedEffect(Unit) { model.search.onInput(text) }
    LaunchedEffect(present) {
        if (!present) onGone()
    }
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
            error?.let { failure ->
                item(key = "add-error") {
                    Text(
                        describe(failure, ChatAction.ADD_MEMBER).text(),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp)
                            .semantics { liveRegion = LiveRegionMode.Polite },
                    )
                }
            }
            userResults(
                state,
                busy = adding,
                onPick = { model.add(it, onBack) },
                onRetry = model.search::retry,
                taken = members,
                takenLabel = alreadyMember,
            )
        }
    }
}
