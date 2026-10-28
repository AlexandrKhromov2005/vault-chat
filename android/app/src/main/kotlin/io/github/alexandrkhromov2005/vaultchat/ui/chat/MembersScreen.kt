package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
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
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MemberItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RosterEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.ui.auth.Message
import io.github.alexandrkhromov2005.vaultchat.ui.auth.text
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.runningFold
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class MembersViewModel(private val chat: ChatSession, private val roomId: String) : ViewModel() {
    init {
        addCloseable(chat.retain())
    }

    val me: String = chat.me

    val room: StateFlow<RoomView> = chat.room(roomId)
        .runningFold(RoomView.Loading as RoomView) { previous, item ->
            when {
                item != null -> RoomView.Present(item)
                previous is RoomView.Present -> RoomView.Gone
                else -> previous
            }
        }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), RoomView.Loading)

    val members: StateFlow<List<MemberItem>?> = chat.membership.members(roomId)
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), null)

    val roster: StateFlow<RosterEntity?> = chat.membership.roster(roomId)
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), null)

    /** Messages of this room that leaving would delete unsent. */
    val unsent: StateFlow<Int> = chat.outbox.outgoing(roomId).map { rows -> rows.count { it.state != OutgoingEntity.SENT } }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), 0)

    private val mutableLoading = MutableStateFlow(false)
    val loading: StateFlow<Boolean> = mutableLoading.asStateFlow()

    private val mutableError = MutableStateFlow<Message?>(null)
    internal val error: StateFlow<Message?> = mutableError.asStateFlow()

    private val mutableBusy = MutableStateFlow<String?>(null)

    /** The member being removed, or the user themselves while leaving. */
    val busy: StateFlow<String?> = mutableBusy.asStateFlow()

    init {
        reload()
        // Events missed while the WebSocket was down are recovered only by
        // reading the roster again (docs/android/rooms.md).
        viewModelScope.launch { chat.sync.completed.drop(1).collect { reload() } }
    }

    fun reload() {
        if (mutableLoading.value) return
        mutableLoading.value = true
        viewModelScope.launch {
            try {
                chat.membership.loadRoster(roomId)
                mutableError.value = null
            } catch (e: ApiException) {
                mutableError.value = describe(e, ChatAction.ROSTER)
            } catch (e: Exception) {
                mutableError.value = Message(R.string.chat_error_local)
            } finally {
                mutableLoading.value = false
            }
        }
    }

    /** Asks for the names of members on screen. */
    fun wantName(userId: String) = chat.sync.wantProfiles(listOf(userId))

    fun remove(userId: String) = act(userId, ChatAction.REMOVE_MEMBER, onDone = {}) {
        chat.membership.removeMember(roomId, userId)
    }

    fun leave(onLeft: () -> Unit) = act(me, ChatAction.LEAVE, onDone = onLeft) { chat.membership.leave(roomId) }

    /** Runs one change at a time; it finishes even if the screen closes meanwhile. */
    private fun act(subject: String, action: ChatAction, onDone: () -> Unit, change: suspend () -> Unit) {
        if (mutableBusy.value != null) return
        mutableBusy.value = subject
        mutableError.value = null
        viewModelScope.launch {
            val failure = withContext(NonCancellable) {
                try {
                    change()
                    null
                } catch (e: ApiException) {
                    e
                }
            }
            mutableBusy.value = null
            if (failure == null) {
                onDone()
                return@launch
            }
            mutableError.value = describe(failure, action)
            // The rights this screen assumed may be wrong.
            if (failure.status == 403 || failure.status == 409) reload()
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MembersScreen(chat: ChatSession, roomId: String, onBack: () -> Unit, onGone: () -> Unit, onAdd: () -> Unit) {
    val model = viewModel { MembersViewModel(chat, roomId) }
    val room by model.room.collectAsStateWithLifecycle()
    val members by model.members.collectAsStateWithLifecycle()
    val roster by model.roster.collectAsStateWithLifecycle()
    val loading by model.loading.collectAsStateWithLifecycle()
    val error by model.error.collectAsStateWithLifecycle()
    val busy by model.busy.collectAsStateWithLifecycle()
    val unsent by model.unsent.collectAsStateWithLifecycle()
    var removing by remember { mutableStateOf<MemberItem?>(null) }
    var leaving by remember { mutableStateOf(false) }

    LaunchedEffect(room) {
        if (room == RoomView.Gone) onGone()
    }
    val present = (room as? RoomView.Present)?.item?.room
    val owner = present?.kind == RoomEntity.CHANNEL && present.ownerId.equals(model.me, ignoreCase = true)
    val list = members.orEmpty()

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.back))
                    }
                },
                title = {
                    TitleWithStatus(
                        stringResource(R.string.members_title),
                        if (roster != null) pluralStringResource(R.plurals.members_count, list.size, list.size) else null,
                    )
                },
            )
        },
    ) { padding ->
        LazyColumn(Modifier.fillMaxSize().padding(padding)) {
            if (loading) item(key = "progress") { LinearProgressIndicator(Modifier.fillMaxWidth()) }
            error?.let { message ->
                item(key = "error") {
                    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
                        Text(
                            message.text(),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.error,
                            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                        )
                        TextButton(onClick = model::reload) { Text(stringResource(R.string.retry)) }
                    }
                }
            }
            if (owner) {
                item(key = "add") {
                    ActionRow(R.drawable.ic_person_add, stringResource(R.string.members_add), onAdd)
                    HorizontalDivider()
                }
            }
            items(list, key = { it.userId }) { member ->
                LaunchedEffect(member.userId, member.known) {
                    if (!member.known && member.userId != model.me) model.wantName(member.userId)
                }
                val isMe = member.userId == model.me
                val isOwner = present?.ownerId.equals(member.userId, ignoreCase = true)
                val name = memberName(member, isMe)
                UserRow(
                    name = name,
                    label = if (isOwner) stringResource(R.string.members_owner) else null,
                    busy = busy == member.userId,
                    trailing = if (owner && !isMe && !isOwner) {
                        {
                            IconButton(onClick = { removing = member }, enabled = busy == null) {
                                Icon(painterResource(R.drawable.ic_close), contentDescription = stringResource(R.string.members_remove))
                            }
                        }
                    } else {
                        null
                    },
                )
            }
            if (roster?.complete == false) {
                item(key = "incomplete") { Note(stringResource(R.string.members_incomplete, list.size)) }
            }
            if (present?.kind == RoomEntity.CHANNEL && !owner) {
                item(key = "leave") {
                    HorizontalDivider()
                    TextButton(
                        onClick = { leaving = true },
                        enabled = busy == null,
                        modifier = Modifier.padding(horizontal = 8.dp, vertical = 8.dp),
                    ) {
                        Text(stringResource(R.string.leave_channel), color = MaterialTheme.colorScheme.error)
                    }
                }
            }
        }
    }

    removing?.let { member ->
        val name = memberName(member, isMe = false)
        ConfirmDialog(
            title = stringResource(R.string.members_remove_title, name),
            text = stringResource(R.string.members_remove_text),
            confirm = stringResource(R.string.members_remove),
            onConfirm = {
                removing = null
                model.remove(member.userId)
            },
            onDismiss = { removing = null },
        )
    }
    if (leaving) {
        val text = stringResource(R.string.leave_text) +
            if (unsent > 0) " " + pluralStringResource(R.plurals.leave_unsent, unsent, unsent) else ""
        ConfirmDialog(
            title = stringResource(R.string.leave_title),
            text = text,
            confirm = stringResource(R.string.leave_channel),
            onConfirm = {
                leaving = false
                model.leave(onGone)
            },
            onDismiss = { leaving = false },
        )
    }
}

@Composable
private fun memberName(member: MemberItem, isMe: Boolean): String = when {
    isMe -> stringResource(R.string.members_you)
    member.username != null -> member.username
    member.known -> stringResource(R.string.sender_deleted)
    else -> stringResource(R.string.sender_unknown)
}

@Composable
internal fun ConfirmDialog(title: String, text: String, confirm: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(text) },
        confirmButton = {
            TextButton(onClick = onConfirm) { Text(confirm, color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
        },
    )
}
