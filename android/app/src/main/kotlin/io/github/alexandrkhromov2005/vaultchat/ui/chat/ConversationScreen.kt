package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLocale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.paging.LoadState
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.paging.insertSeparators
import androidx.paging.map
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSession
import io.github.alexandrkhromov2005.vaultchat.core.chat.ConnectionStatus
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomListItem
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.runningFold
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** A row of a conversation, newest first. */
sealed interface ChatRow {
    val key: String

    data class Message(val item: MessageItem) : ChatRow {
        override val key: String get() = "m${item.message.sequence}"
    }

    /** The heading of the day of the message below it. */
    data class Day(val epochDay: Long, val beforeSequence: Long) : ChatRow {
        override val key: String get() = "d$beforeSequence"
    }
}

/** The room as the screen sees it: still loading, present, or gone. */
sealed interface RoomView {
    data object Loading : RoomView

    data class Present(val item: RoomListItem) : RoomView

    /** The user is no longer a member, or the room was never theirs. */
    data object Gone : RoomView
}

class ConversationViewModel(private val chat: ChatSession, private val roomId: String) : ViewModel() {
    init {
        addCloseable(chat.retain())
    }

    val room: StateFlow<RoomView> = chat.room(roomId)
        .runningFold(RoomView.Loading as RoomView) { previous, item ->
            when {
                item != null -> RoomView.Present(item)
                // A room that disappears while open was lost; one that never
                // appears is only reported once loading it failed.
                previous is RoomView.Present -> RoomView.Gone
                else -> previous
            }
        }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), RoomView.Loading)

    val rows: Flow<PagingData<ChatRow>> = chat.messages(roomId)
        .map { page ->
            page.map { ChatRow.Message(it) as ChatRow }
                .insertSeparators { newer, older ->
                    // A heading above the oldest message of each day; the list is
                    // newest first, so it goes after that message.
                    val above = (newer as? ChatRow.Message)?.item?.message ?: return@insertSeparators null
                    val below = (older as? ChatRow.Message)?.item?.message
                    val day = epochDay(above.createdAt)
                    if (below == null || epochDay(below.createdAt) != day) ChatRow.Day(day, above.sequence) else null
                }
        }
        .cachedIn(viewModelScope)

    val connection: StateFlow<ConnectionStatus> = chat.connection

    /** Marks what is shown as read while the screen is in front. */
    fun markRead() {
        viewModelScope.launch {
            try {
                chat.sync.markRead(roomId)
            } catch (e: Exception) {
                // Only the unread count is affected.
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConversationScreen(chat: ChatSession, roomId: String, onBack: () -> Unit) {
    val model = viewModel { ConversationViewModel(chat, roomId) }
    val room by model.room.collectAsStateWithLifecycle()
    val connection by model.connection.collectAsStateWithLifecycle()
    val rows = model.rows.collectAsLazyPagingItems()

    LaunchedEffect(room) {
        if (room == RoomView.Gone) onBack()
    }
    // Opening the room, and every new message while it is in front, marks it read.
    val newest = if (rows.itemCount > 0) rows.peek(0)?.key else null
    LifecycleResumeEffect(newest) {
        model.markRead()
        onPauseOrDispose { }
    }

    val present = (room as? RoomView.Present)?.item
    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(painterResource(R.drawable.ic_arrow_back), contentDescription = stringResource(R.string.back))
                    }
                },
                title = {
                    if (present != null) {
                        TitleWithStatus(
                            roomTitle(present.room, present.peerName),
                            connectionText(connection) ?: roomKind(present.room),
                        )
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            Messages(rows, me = chat.me, channel = present?.room?.kind == RoomEntity.CHANNEL)
        }
    }
}

@Composable
private fun Messages(rows: LazyPagingItems<ChatRow>, me: String, channel: Boolean) {
    val listState = rememberLazyListState()
    val refresh = rows.loadState.refresh

    // Follow new messages while the newest one is in view.
    val newest = if (rows.itemCount > 0) rows.peek(0)?.key else null
    LaunchedEffect(newest) {
        if (listState.firstVisibleItemIndex <= 1) listState.animateScrollToItem(0)
    }

    if (rows.itemCount == 0) {
        when {
            refresh is LoadState.Loading || rows.loadState.mediator?.refresh is LoadState.Loading -> Centered(Modifier.fillMaxSize()) {
                CircularProgressIndicator()
            }
            refresh is LoadState.Error -> Centered(Modifier.fillMaxSize()) {
                LoadError(refresh.error, onRetry = rows::retry)
            }
            else -> Centered(Modifier.fillMaxSize()) {
                Text(
                    stringResource(R.string.conversation_empty),
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        return
    }

    BoxWithConstraints(Modifier.fillMaxSize()) {
        // Bubbles leave a margin on the other side, as in other messengers.
        val bubbleWidth = maxWidth * BUBBLE_WIDTH
        MessageList(rows, listState, me, channel, bubbleWidth)
    }
}

@Composable
private fun MessageList(rows: LazyPagingItems<ChatRow>, listState: LazyListState, me: String, channel: Boolean, bubbleWidth: Dp) {
    val append = rows.loadState.append
    LazyColumn(
        state = listState,
        reverseLayout = true,
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
    ) {
        items(count = rows.itemCount, key = { index -> rows.peek(index)?.key ?: index }) { index ->
            when (val row = rows[index]) {
                is ChatRow.Message -> {
                    val older = if (index + 1 < rows.itemCount) rows.peek(index + 1) else null
                    val message = row.item.message
                    // A sender's name heads each run of their messages in channels.
                    val continues = older is ChatRow.Message && older.item.message.senderId == message.senderId &&
                        message.createdAt - older.item.message.createdAt < GROUP_WINDOW_MS
                    Bubble(
                        row.item,
                        own = message.senderId == me,
                        showSender = channel && !continues && message.senderId != me,
                        maxWidth = bubbleWidth,
                    )
                }
                is ChatRow.Day -> DayHeading(row.epochDay)
                null -> Unit
            }
        }
        when (append) {
            is LoadState.Loading -> item(key = "older-loading") {
                Centered(Modifier.padding(12.dp)) { CircularProgressIndicator(Modifier.size(24.dp)) }
            }
            is LoadState.Error -> item(key = "older-error") {
                Centered(Modifier.padding(12.dp)) { LoadError(append.error, onRetry = rows::retry) }
            }
            else -> Unit
        }
    }
}

private const val GROUP_WINDOW_MS = 5 * 60 * 1000L

@Composable
private fun Centered(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Box(modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { content() }
}

@Composable
private fun LoadError(error: Throwable, onRetry: () -> Unit) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(horizontal = 24.dp)) {
        Text(
            stringResource(if ((error as? ApiException)?.code == ApiException.NETWORK) R.string.history_offline else R.string.history_failed),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        TextButton(onClick = onRetry) { Text(stringResource(R.string.retry)) }
    }
}

@Composable
private fun DayHeading(epochDay: Long) {
    Text(
        formatDay(epochDay),
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
        modifier = Modifier.fillMaxWidth().padding(vertical = 12.dp).semantics { heading() },
    )
}

@Composable
private fun Bubble(item: MessageItem, own: Boolean, showSender: Boolean, maxWidth: Dp) {
    val message = item.message
    val sender = when {
        own -> stringResource(R.string.sender_you)
        item.senderName != null -> item.senderName
        item.senderKnown -> stringResource(R.string.sender_deleted)
        else -> stringResource(R.string.sender_unknown)
    }
    val time = formatTime(message.createdAt, LocalLocale.current.platformLocale)
    val attachments = message.attachmentCount
    val attachmentText = if (attachments > 0) pluralStringResource(R.plurals.attachments, attachments, attachments) else null
    val description = listOfNotNull(sender, message.text.takeIf { it.isNotEmpty() }, attachmentText, time).joinToString(", ")

    Row(
        Modifier.fillMaxWidth().padding(vertical = 2.dp),
        horizontalArrangement = if (own) Arrangement.End else Arrangement.Start,
    ) {
        Column(
            Modifier
                .widthIn(max = maxWidth)
                .clearAndSetSemantics { contentDescription = description }
                .background(
                    if (own) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerHigh,
                    RoundedCornerShape(18.dp),
                )
                .padding(horizontal = 12.dp, vertical = 8.dp),
        ) {
            val content = if (own) MaterialTheme.colorScheme.onPrimaryContainer else MaterialTheme.colorScheme.onSurface
            if (showSender) {
                Text(sender, style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
            }
            if (message.text.isNotEmpty()) {
                Text(message.text, style = MaterialTheme.typography.bodyLarge, color = content)
            }
            if (attachmentText != null) {
                Text(attachmentText, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Text(
                time,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.align(Alignment.End),
            )
        }
    }
}

private const val BUBBLE_WIDTH = 0.8f
