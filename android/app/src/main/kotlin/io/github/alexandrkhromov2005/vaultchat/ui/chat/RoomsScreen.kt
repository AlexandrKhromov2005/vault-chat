package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLocale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSession
import io.github.alexandrkhromov2005.vaultchat.core.chat.ConnectionStatus
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomListItem
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class RoomsViewModel(
    private val chat: ChatSession,
    private val logout: suspend () -> Unit,
    private val appScope: CoroutineScope,
) : ViewModel() {
    init {
        addCloseable(chat.retain())
    }

    /** Null until the database has answered, so that an empty list is not shown too early. */
    val rooms: StateFlow<List<RoomListItem>?> =
        chat.rooms.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), null)

    val connection: StateFlow<ConnectionStatus> = chat.connection

    val refreshing: StateFlow<Boolean> = chat.sync.status.map { it.running }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), false)

    fun refresh() = chat.sync.requestSync()

    /** Messages that signing out would delete unsent. */
    val unsent: StateFlow<Int> = chat.outbox.unsentCount
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), 0)

    // Ending the session on the server must not stop when this screen goes away.
    fun logout() {
        appScope.launch { logout.invoke() }
    }
}

internal const val STOP_TIMEOUT_MS = 5_000L

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RoomsScreen(
    chat: ChatSession,
    username: String,
    server: String,
    logout: suspend () -> Unit,
    appScope: CoroutineScope,
    openRoom: (String) -> Unit,
    newChat: () -> Unit,
) {
    val model = viewModel { RoomsViewModel(chat, logout, appScope) }
    val rooms by model.rooms.collectAsStateWithLifecycle()
    val connection by model.connection.collectAsStateWithLifecycle()
    val refreshing by model.refreshing.collectAsStateWithLifecycle()
    val unsent by model.unsent.collectAsStateWithLifecycle()
    var confirmLogout by remember { mutableStateOf(false) }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { TitleWithStatus(stringResource(R.string.rooms_title), connectionText(connection)) },
                actions = {
                    AccountMenu(username, server, onLogout = { if (unsent > 0) confirmLogout = true else model.logout() })
                },
            )
        },
        floatingActionButton = {
            FloatingActionButton(onClick = newChat) {
                Icon(painterResource(R.drawable.ic_edit), contentDescription = stringResource(R.string.new_chat))
            }
        },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = refreshing,
            onRefresh = model::refresh,
            modifier = Modifier.fillMaxSize().padding(padding),
        ) {
            val list = rooms
            when {
                list == null -> Unit
                list.isEmpty() -> EmptyRooms()
                else -> LazyColumn(Modifier.fillMaxSize()) {
                    items(list, key = { it.room.roomId }) { item ->
                        RoomRow(item, me = chat.me, onClick = { openRoom(item.room.roomId) })
                    }
                }
            }
        }
    }
    if (confirmLogout) {
        LogoutDialog(
            unsent,
            onConfirm = {
                confirmLogout = false
                model.logout()
            },
            onDismiss = { confirmLogout = false },
        )
    }
}

/** Signing out deletes the queue with the rest of the data; say so first. */
@Composable
private fun LogoutDialog(unsent: Int, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.logout_title)) },
        text = { Text(pluralStringResource(R.plurals.logout_unsent, unsent, unsent)) },
        confirmButton = {
            TextButton(onClick = onConfirm) { Text(stringResource(R.string.logout)) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.logout_cancel)) }
        },
    )
}

/** A screen title with the connection state below it, when there is one to show. */
@Composable
fun TitleWithStatus(title: String, status: String?) {
    Column {
        Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (status != null) {
            Text(
                status,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
            )
        }
    }
}

@Composable
private fun AccountMenu(username: String, server: String, onLogout: () -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }) {
            Icon(painterResource(R.drawable.ic_more_vert), contentDescription = stringResource(R.string.menu))
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp).widthIn(max = 280.dp)) {
                Text(username, style = MaterialTheme.typography.titleSmall)
                Text(
                    server,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            HorizontalDivider()
            DropdownMenuItem(
                text = { Text(stringResource(R.string.logout)) },
                onClick = {
                    open = false
                    onLogout()
                },
            )
        }
    }
}

@Composable
private fun EmptyRooms() {
    // PullToRefreshBox needs scrollable content to start a refresh.
    LazyColumn(Modifier.fillMaxSize()) {
        item {
            Text(
                stringResource(R.string.rooms_empty),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 32.dp),
            )
        }
    }
}

@Composable
private fun RoomRow(item: RoomListItem, me: String, onClick: () -> Unit) {
    val title = roomTitle(item.room, item.peerName)
    // A queued message is newer than any stored one.
    val time = item.pendingCreatedAt ?: item.lastCreatedAt ?: item.room.lastMessageAt
    val preview = preview(item, me)
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .heightIn(min = 72.dp)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Avatar(title)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    title,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = if (item.unread > 0) FontWeight.SemiBold else FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (time != null) {
                    Spacer(Modifier.size(8.dp))
                    Text(
                        formatListTime(time, LocalLocale.current.platformLocale),
                        style = MaterialTheme.typography.labelMedium,
                        color = if (item.unread > 0) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                PendingIcon(item.pendingState)
                Text(
                    preview,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (item.unread > 0) {
                    Spacer(Modifier.size(8.dp))
                    UnreadBadge(item.unread, more = item.unreadMore)
                }
            }
        }
    }
}

/** The state of the room's last queued message, before its preview. */
@Composable
private fun PendingIcon(state: String?) {
    val (icon, tint) = when (state) {
        OutgoingEntity.QUEUED -> R.drawable.ic_schedule to MaterialTheme.colorScheme.onSurfaceVariant
        OutgoingEntity.SENT -> R.drawable.ic_done to MaterialTheme.colorScheme.onSurfaceVariant
        OutgoingEntity.FAILED -> R.drawable.ic_error to MaterialTheme.colorScheme.error
        else -> return
    }
    val description = stringResource(
        when (state) {
            OutgoingEntity.FAILED -> R.string.message_failed
            OutgoingEntity.SENT -> R.string.message_sent
            else -> R.string.message_queued
        },
    )
    Icon(painterResource(icon), contentDescription = description, tint = tint, modifier = Modifier.padding(end = 4.dp).size(16.dp))
}

@Composable
private fun preview(item: RoomListItem, me: String): String {
    item.pendingText?.lineSequence()?.firstOrNull { it.isNotBlank() }?.trim()?.let {
        return stringResource(R.string.preview_own, it)
    }
    val text = item.lastText?.lineSequence()?.firstOrNull { it.isNotBlank() }?.trim()
    val body = when {
        !text.isNullOrEmpty() -> text
        !item.lastAttachments.isNullOrEmpty() -> stringResource(R.string.preview_attachment)
        else -> return roomKind(item.room)
    }
    return if (item.lastSenderId == me) stringResource(R.string.preview_own, body) else body
}

@Composable
fun Avatar(name: String, size: Int = 48) {
    Box(
        Modifier.size(size.dp).background(MaterialTheme.colorScheme.secondaryContainer, CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            initial(name),
            style = if (size >= 44) MaterialTheme.typography.titleMedium else MaterialTheme.typography.titleSmall,
            color = MaterialTheme.colorScheme.onSecondaryContainer,
        )
    }
}

@Composable
private fun UnreadBadge(count: Int, more: Boolean) {
    val label = when {
        count > 99 -> "99+"
        more -> "$count+"
        else -> count.toString()
    }
    val description = stringResource(R.string.unread_description, count)
    Box(
        Modifier
            .heightIn(min = 22.dp)
            .widthIn(min = 22.dp)
            .background(MaterialTheme.colorScheme.primary, CircleShape)
            .padding(horizontal = 6.dp)
            .clearAndSetSemantics { contentDescription = description },
        contentAlignment = Alignment.Center,
    ) {
        Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onPrimary)
    }
}
