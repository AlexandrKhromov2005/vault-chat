package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.paging.ExperimentalPagingApi
import androidx.paging.LoadType
import androidx.paging.Pager
import androidx.paging.PagingConfig
import androidx.paging.PagingData
import androidx.paging.PagingState
import androidx.paging.RemoteMediator
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomListItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.TimelineEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import retrofit2.Response
import java.io.Closeable
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/** What the connection indicator shows. */
enum class ConnectionStatus { ONLINE, UPDATING, CONNECTING, OFFLINE, UNREACHABLE }

/** What a session needs from the rest of the app. */
interface ChatSessionDeps {
    val foreground: StateFlow<Boolean>
    val network: StateFlow<Any?>
    val elapsed: () -> Long

    suspend fun <T : Any> send(session: SessionState.SignedIn, request: suspend GatewayApi.(String) -> Response<T>): T

    suspend fun grant(session: SessionState.SignedIn, minValidityMs: Long): AccessGrant

    suspend fun renew(session: SessionState.SignedIn, rejected: String): AccessGrant

    fun openSocket(session: SessionState.SignedIn, listener: WebSocketListener): WebSocket

    /** Makes sure queued messages are sent even if the app leaves the screen (OutboxWorker). */
    fun scheduleBackgroundSend() {}
}

/**
 * The chat of one signed-in session: its database, sync and WebSocket. The
 * WebSocket runs while the app is visible and a network is available; a new
 * network reopens it. [end] stops everything; the database is closed once no
 * screen retains the session any more (see [retain]).
 */
class ChatSession(
    val signedIn: SessionState.SignedIn,
    val databaseName: String,
    private val database: ChatDatabase,
    private val deps: ChatSessionDeps,
    private val onReleased: (ChatSession) -> Unit,
) {
    private val scope = CoroutineScope(
        SupervisorJob() + Dispatchers.Default + CoroutineExceptionHandler { _, _ ->
            // A failed background task of this session; the next sync recovers.
        },
    )
    private val dao: ChatDao = database.dao()
    val me: String = signedIn.userId

    private val requests = object : ChatRequests {
        override suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T =
            deps.send(signedIn, request)
    }

    val sync = ChatSync(dao, me, requests, scope, deps.elapsed)

    val outbox = Outbox(
        dao, me, requests, sync, scope, deps.network,
        onQueued = deps::scheduleBackgroundSend,
        budget = RequestBudget(Outbox.SENDS_PER_MINUTE, Outbox.SEND_BURST, deps.elapsed),
    )

    val membership = Membership(dao, me, sync)

    val realtime = Realtime(
        userId = me,
        tokens = object : AccessTokens {
            override suspend fun grant(minValidityMs: Long) = deps.grant(signedIn, minValidityMs)

            override suspend fun renew(rejected: String) = deps.renew(signedIn, rejected)
        },
        open = { listener -> deps.openSocket(signedIn, listener) },
        elapsed = deps.elapsed,
        onReady = {
            sync.requestSync()
            outbox.kick()
        },
        onFrame = sync::onEvent,
    )

    val connection: StateFlow<ConnectionStatus> =
        combine(deps.foreground, deps.network, realtime.state, sync.status) { _, network, socket, status ->
            when {
                network == null -> ConnectionStatus.OFFLINE
                socket == RealtimeState.FAILED -> ConnectionStatus.UNREACHABLE
                socket != RealtimeState.LIVE && status.failure != null && !status.running -> ConnectionStatus.UNREACHABLE
                socket != RealtimeState.LIVE -> ConnectionStatus.CONNECTING
                status.running -> ConnectionStatus.UPDATING
                else -> ConnectionStatus.ONLINE
            }
        }.stateIn(scope, SharingStarted.Eagerly, ConnectionStatus.CONNECTING)

    private val retained = AtomicInteger()
    private val ended = AtomicBoolean()

    init {
        scope.launch {
            combine(deps.foreground, deps.network) { visible, network -> if (visible) network else null }
                .distinctUntilChanged()
                .collectLatest { network ->
                    if (network == null) return@collectLatest
                    outbox.kick()
                    // A sync also runs without the WebSocket, if it does not
                    // become live soon; its ready triggers one otherwise.
                    launch {
                        delay(SYNC_WITHOUT_SOCKET_MS)
                        if (realtime.state.value != RealtimeState.LIVE) sync.requestSync()
                    }
                    realtime.run()
                }
        }
    }

    val rooms: Flow<List<RoomListItem>> get() = dao.roomList(me)

    fun room(roomId: String): Flow<RoomListItem?> = dao.roomItem(me, roomId)

    fun timeline(roomId: String): Flow<TimelineEntity?> = dao.timelineFlow(roomId)

    /** Messages of a room, newest first, read from the database and loaded on demand. */
    @OptIn(ExperimentalPagingApi::class)
    fun messages(roomId: String): Flow<PagingData<MessageItem>> = Pager(
        config = PagingConfig(pageSize = sync.limits.history, prefetchDistance = 20, enablePlaceholders = false),
        remoteMediator = MessagesMediator(sync, roomId),
        pagingSourceFactory = { dao.messages(roomId) },
    ).flow

    /**
     * Keeps the database open until the returned handle is closed. Screens
     * retain the session for as long as they may read from it.
     */
    fun retain(): Closeable {
        retained.incrementAndGet()
        val closed = AtomicBoolean()
        return Closeable {
            if (closed.compareAndSet(false, true) && retained.decrementAndGet() == 0 && ended.get()) release()
        }
    }

    /**
     * Stops the session. With [wipe], its data is deleted from the database at
     * once; the file itself goes when the database is closed.
     */
    fun end(wipe: Boolean, background: CoroutineScope) {
        if (!ended.compareAndSet(false, true)) return
        scope.cancel()
        background.launch(Dispatchers.IO) {
            if (wipe) runCatching { database.clearAllTables() }
            if (retained.get() == 0) release()
        }
    }

    private val released = AtomicBoolean()

    private fun release() {
        if (!released.compareAndSet(false, true)) return
        onReleased(this)
    }

    /** Closes the database; only after [end] and once nothing retains it. */
    internal fun close() {
        database.close()
    }

    companion object {
        private const val SYNC_WITHOUT_SOCKET_MS = 5_000L
    }
}

/**
 * Loads a room's messages into the database for the pager: the latest page
 * or a catch-up when the room is opened, older pages as the reader scrolls.
 * Newer messages arrive through sync, never by paging.
 */
@OptIn(ExperimentalPagingApi::class)
private class MessagesMediator(private val sync: ChatSync, private val roomId: String) :
    RemoteMediator<Int, MessageItem>() {

    override suspend fun initialize(): InitializeAction = InitializeAction.LAUNCH_INITIAL_REFRESH

    override suspend fun load(loadType: LoadType, state: PagingState<Int, MessageItem>): MediatorResult = try {
        when (loadType) {
            LoadType.REFRESH -> {
                sync.openRoom(roomId)
                MediatorResult.Success(endOfPaginationReached = false)
            }
            LoadType.PREPEND -> MediatorResult.Success(endOfPaginationReached = true)
            LoadType.APPEND -> MediatorResult.Success(endOfPaginationReached = sync.loadOlder(roomId))
        }
    } catch (e: ApiException) {
        MediatorResult.Error(e)
    }
}
