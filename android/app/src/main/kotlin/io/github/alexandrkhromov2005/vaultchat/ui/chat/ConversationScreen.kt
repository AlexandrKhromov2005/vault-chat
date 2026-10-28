package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.ClipData
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalLocale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardCapitalization
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
import io.github.alexandrkhromov2005.vaultchat.core.chat.MAX_TEXT_CODE_POINTS
import io.github.alexandrkhromov2005.vaultchat.core.chat.MessageText
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomListItem
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.runningFold
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

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

    /** The oldest stored message; it grows when the room was reset to its latest page. */
    val oldest: StateFlow<Long?> = chat.timeline(roomId).map { it?.oldest }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), null)

    /** Messages of this room that are queued, sent but not yet in the stored history, or failed. */
    val outgoing: StateFlow<List<OutgoingEntity>> = chat.outbox.outgoing(roomId)
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MS), emptyList())

    /** Queues [text]; it is stored even if the screen closes at once. */
    fun send(text: String) = outboxWork { chat.outbox.send(roomId, text) }

    fun retry(clientMessageId: String) = outboxWork { chat.outbox.retry(clientMessageId) }

    fun delete(clientMessageId: String) = outboxWork { chat.outbox.delete(clientMessageId) }

    private fun outboxWork(work: suspend () -> Unit) {
        viewModelScope.launch {
            withContext(NonCancellable) {
                try {
                    work()
                } catch (e: Exception) {
                    // A database failure; nothing else to do here.
                }
            }
        }
    }

    /** Marks the room read up to its newest message, which the reader sees. */
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
    val outgoing by model.outgoing.collectAsStateWithLifecycle()

    LaunchedEffect(room) {
        if (room == RoomView.Gone) onBack()
    }
    // A reset drops the older history the pager has already seen to its end;
    // it must start over to page through it again.
    val oldest by model.oldest.collectAsStateWithLifecycle()
    var seenOldest by remember { mutableStateOf<Long?>(null) }
    LaunchedEffect(oldest) {
        val previous = seenOldest
        val current = oldest
        seenOldest = current
        if (previous != null && current != null && current > previous) rows.refresh()
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
        bottomBar = {
            if (present != null) Composer(onSend = model::send)
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            Messages(
                rows,
                outgoing,
                me = chat.me,
                channel = present?.room?.kind == RoomEntity.CHANNEL,
                onNewestSeen = model::markRead,
                onRetry = model::retry,
                onDelete = model::delete,
            )
        }
    }
}

/**
 * The message field. Text is sent as typed, without trimming; the send
 * button is enabled only for text the server accepts (MessageText).
 */
@Composable
private fun Composer(onSend: (String) -> Unit) {
    var text by rememberSaveable { mutableStateOf("") }
    val problem = MessageText.check(text)
    val length = MessageText.length(text)
    Surface(color = MaterialTheme.colorScheme.surfaceContainer) {
        Row(
            Modifier.fillMaxWidth().navigationBarsPadding().imePadding().padding(horizontal = 8.dp, vertical = 6.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            Column(Modifier.weight(1f)) {
                TextField(
                    value = text,
                    onValueChange = { text = it },
                    placeholder = { Text(stringResource(R.string.composer_hint)) },
                    maxLines = COMPOSER_LINES,
                    shape = RoundedCornerShape(24.dp),
                    colors = TextFieldDefaults.colors(
                        focusedIndicatorColor = Color.Transparent,
                        unfocusedIndicatorColor = Color.Transparent,
                        focusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHighest,
                        unfocusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHighest,
                    ),
                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                    modifier = Modifier.fillMaxWidth(),
                )
                val hint = when {
                    problem == MessageText.Problem.INVALID_CHARACTER -> stringResource(R.string.composer_invalid)
                    length > MAX_TEXT_CODE_POINTS - COUNTER_FROM_END ->
                        stringResource(R.string.composer_too_long, length, MAX_TEXT_CODE_POINTS)
                    else -> null
                }
                if (hint != null) {
                    Text(
                        hint,
                        style = MaterialTheme.typography.labelSmall,
                        color = if (problem != null) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(start = 16.dp, top = 2.dp),
                    )
                }
            }
            IconButton(
                onClick = {
                    onSend(text)
                    text = ""
                },
                enabled = problem == null,
            ) {
                Icon(
                    painterResource(R.drawable.ic_send),
                    contentDescription = stringResource(R.string.composer_send),
                    tint = if (problem == null) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

private const val COMPOSER_LINES = 6

// The counter appears this close to the limit.
private const val COUNTER_FROM_END = 200

@Composable
private fun Messages(
    rows: LazyPagingItems<ChatRow>,
    outgoing: List<OutgoingEntity>,
    me: String,
    channel: Boolean,
    onNewestSeen: () -> Unit,
    onRetry: (String) -> Unit,
    onDelete: (String) -> Unit,
) {
    val listState = rememberLazyListState()
    val refresh = rows.loadState.refresh
    // Outgoing messages are the first items; the newest stored one follows them.
    val outgoingCount by rememberUpdatedState(outgoing.size)

    // Follow new messages while the newest one is in view.
    val newest = if (rows.itemCount > 0) rows.peek(0)?.key else null
    LaunchedEffect(newest) {
        if (listState.firstVisibleItemIndex <= outgoingCount + 1) listState.animateScrollToItem(0)
    }
    // The room is read once its newest message is on screen, not while the
    // reader is scrolled up in the history.
    val atNewest by remember { derivedStateOf { listState.firstVisibleItemIndex <= outgoingCount } }
    LifecycleResumeEffect(newest, atNewest) {
        if (newest != null && atNewest) onNewestSeen()
        onPauseOrDispose { }
    }
    // A message the user has just queued is always brought into view.
    val lastQueued = outgoing.lastOrNull()?.clientMessageId
    LaunchedEffect(lastQueued) {
        if (lastQueued != null) listState.animateScrollToItem(0)
    }

    if (rows.itemCount == 0 && outgoing.isEmpty()) {
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
        MessageList(rows, outgoing, listState, me, channel, bubbleWidth, onRetry, onDelete)
    }
}

@Composable
private fun MessageList(
    rows: LazyPagingItems<ChatRow>,
    outgoing: List<OutgoingEntity>,
    listState: LazyListState,
    me: String,
    channel: Boolean,
    bubbleWidth: Dp,
    onRetry: (String) -> Unit,
    onDelete: (String) -> Unit,
) {
    val append = rows.loadState.append
    LazyColumn(
        state = listState,
        reverseLayout = true,
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
    ) {
        // Outgoing messages come after every stored one, in queue order; the
        // layout is reversed, so the last of them is the first item.
        items(outgoing.asReversed(), key = { "o${it.clientMessageId}" }) { row ->
            OutgoingBubble(row, bubbleWidth, onRetry, onDelete)
        }
        items(count = rows.itemCount, key = { index -> rows.peek(index)?.key ?: index }) { index ->
            when (val row = rows[index]) {
                is ChatRow.Message -> {
                    val older = if (index + 1 < rows.itemCount) rows.peek(index + 1) else null
                    val message = row.item.message
                    // A sender's name heads each run of their messages in channels.
                    val continues = older is ChatRow.Message && older.item.message.senderId == message.senderId &&
                        message.createdAt - older.item.message.createdAt < GROUP_WINDOW_MICROS
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

private const val GROUP_WINDOW_MICROS = 5 * 60 * 1_000_000L

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
            Footer(time, if (own) Delivery.SENT else null, Modifier.align(Alignment.End))
        }
    }
}

/** What the footer of an own message shows. */
private enum class Delivery { QUEUED, SENT, FAILED }

@Composable
private fun Footer(time: String, delivery: Delivery?, modifier: Modifier = Modifier) {
    Row(modifier, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(time, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        when (delivery) {
            Delivery.QUEUED -> StatusIcon(R.drawable.ic_schedule, MaterialTheme.colorScheme.onSurfaceVariant)
            Delivery.SENT -> StatusIcon(R.drawable.ic_done, MaterialTheme.colorScheme.onSurfaceVariant)
            Delivery.FAILED -> StatusIcon(R.drawable.ic_error, MaterialTheme.colorScheme.error)
            null -> Unit
        }
    }
}

// Decorative: the bubble's description already names the state.
@Composable
private fun StatusIcon(icon: Int, tint: Color) {
    Icon(painterResource(icon), contentDescription = null, tint = tint, modifier = Modifier.size(14.dp))
}

/**
 * A message of the outgoing queue. A failed one opens a menu to send it
 * again, copy its text or delete it.
 */
@Composable
private fun OutgoingBubble(row: OutgoingEntity, maxWidth: Dp, onRetry: (String) -> Unit, onDelete: (String) -> Unit) {
    val delivery = when (row.state) {
        OutgoingEntity.SENT -> Delivery.SENT
        OutgoingEntity.FAILED -> Delivery.FAILED
        else -> Delivery.QUEUED
    }
    val failure = when {
        delivery != Delivery.FAILED -> null
        row.error == OutgoingEntity.ERROR_NO_ACCESS -> stringResource(R.string.message_failed_no_access)
        else -> stringResource(R.string.message_failed_rejected)
    }
    val state = failure ?: stringResource(if (delivery == Delivery.SENT) R.string.message_sent else R.string.message_queued)
    val time = formatTime(row.createdAt, LocalLocale.current.platformLocale)
    val description = listOf(stringResource(R.string.sender_you), row.text, time, state).joinToString(", ")
    var menu by remember { mutableStateOf(false) }
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()

    Column(Modifier.fillMaxWidth().padding(vertical = 2.dp), horizontalAlignment = Alignment.End) {
        Box {
            Column(
                Modifier
                    .widthIn(max = maxWidth)
                    .background(MaterialTheme.colorScheme.primaryContainer, RoundedCornerShape(18.dp))
                    .then(if (failure != null) Modifier.clickable { menu = true } else Modifier)
                    .clearAndSetSemantics { contentDescription = description }
                    .padding(horizontal = 12.dp, vertical = 8.dp),
            ) {
                Text(row.text, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onPrimaryContainer)
                Footer(time, delivery, Modifier.align(Alignment.End))
            }
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.message_retry)) },
                    onClick = {
                        menu = false
                        onRetry(row.clientMessageId)
                    },
                )
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.message_copy)) },
                    onClick = {
                        menu = false
                        scope.launch { clipboard.setClipEntry(ClipEntry(ClipData.newPlainText(null, row.text))) }
                    },
                )
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.message_delete)) },
                    onClick = {
                        menu = false
                        onDelete(row.clientMessageId)
                    },
                )
            }
        }
        if (failure != null) {
            Text(
                failure,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(top = 2.dp, end = 4.dp),
            )
        }
    }
}

private const val BUBBLE_WIDTH = 0.8f
