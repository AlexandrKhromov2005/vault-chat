package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ProfileEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.RoomDto
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.sync.withPermit
import retrofit2.Response
import java.util.concurrent.ConcurrentHashMap

/** Sends a request for the signed-in session and returns its body. */
interface ChatRequests {
    suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T
}

/** The state of synchronisation, for the connection indicator. */
data class SyncStatus(val running: Boolean, val failure: ApiException?)

/**
 * Brings the chat database up to date with the Gateway and applies live
 * events (docs/android/sync.md).
 *
 * Changes to the room list are serialised by [listLock], so that a room an
 * event adds while a listing is under way is not deleted as unlisted.
 * Background work loads or catches up each room under its own lock, which
 * only saves duplicate requests; opening a room and older pages take none,
 * since the database keeps the block contiguous whatever arrives first. Refreshes that live events
 * ask for are coalesced per room.
 *
 * Requests run at most [CONCURRENT_REQUESTS] at a time and pause together
 * after a 429. Background work also waits for [RequestBudget], which keeps
 * sync below the Gateway's limit per user; requests the user waits for,
 * opening a room and reading older history, do not wait for it, and
 * profiles leave a reserve for everything else. Rooms that events add are
 * read outside the event loop and outside [listLock], so that waiting for
 * the budget holds up neither live events nor the room list.
 */
class ChatSync(
    private val dao: ChatDao,
    private val me: String,
    private val requests: ChatRequests,
    private val scope: CoroutineScope,
    /** A monotonic clock in milliseconds, for rate-limit pauses. */
    private val clock: () -> Long,
    val limits: Limits = Limits(),
) {
    /** Page sizes and budgets of docs/android/sync.md. */
    data class Limits(
        val roomPage: Int = 100,
        val roomPages: Int = 50,
        val preloadedRooms: Int = 10,
        val history: Int = 50,
        val catchUp: Int = 100,
        val catchUpPages: Int = 10,
        /** Half of the Gateway's default per-user limit (120/min, burst 30). */
        val requestsPerMinute: Int = 60,
        val burst: Int = 15,
        /** Tokens that profile requests leave to the rest of sync. */
        val profileReserve: Int = 5,
    )

    /**
     * Whether the user waits for a request; otherwise whether it keeps the
     * stored data correct, or only fills in names.
     */
    private enum class Priority { USER, BACKGROUND, PROFILE }

    /** A refresh of a room that live events asked for; see [scheduleRefresh]. */
    private class PendingRefresh(var readThrough: Long?) {
        var again = false
    }

    private val listLock = Mutex()
    private val syncLock = Mutex()
    private val roomLocks = ConcurrentHashMap<String, Mutex>()
    private val connections = Semaphore(CONCURRENT_REQUESTS)
    private val rate = RequestBudget(limits.requestsPerMinute, limits.burst, clock)
    private val pending = HashMap<String, PendingRefresh>()

    /** A room being added after events; see [addRoom]. */
    private class PendingAdd(var readThrough: Long?) {
        var again = false
    }

    private val adding = HashMap<String, PendingAdd>()

    // How often the user has left each room; a room read from the server
    // before the user left it is not stored afterwards.
    private val departures = ConcurrentHashMap<String, Long>()
    private val profilesInFlight: MutableSet<String> = ConcurrentHashMap.newKeySet()

    @Volatile
    private var pausedUntil = 0L

    private val mutableStatus = MutableStateFlow(SyncStatus(running = false, failure = null))
    val status: StateFlow<SyncStatus> = mutableStatus.asStateFlow()

    // A request during a sync runs one more sync after it.
    private val syncRequests = Channel<Unit>(Channel.CONFLATED)

    // Events are applied in the order they arrived. The buffer is far larger
    // than the Gateway's own per-connection queue; if it still overflows, the
    // events are recovered by a sync.
    private val events = Channel<Frame>(EVENT_BUFFER)

    init {
        scope.launch { for (request in syncRequests) runSync() }
        scope.launch {
            for (event in events) {
                try {
                    apply(event)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // The event is lost; a sync recovers it.
                    requestSync()
                }
            }
        }
    }

    /** Asks for a full sync; requests during a sync coalesce into one more. */
    fun requestSync() {
        syncRequests.trySend(Unit)
    }

    /** Queues a live event from the WebSocket. */
    fun onEvent(frame: Frame) {
        if (events.trySend(frame).isFailure) requestSync()
    }

    /**
     * Loads a room that is being opened, or catches it up. It does not wait
     * for background work on the same room: the database keeps the block
     * contiguous whichever answer comes first, and the user is waiting.
     */
    suspend fun openRoom(roomId: String) {
        loadOrCatchUp(roomId, Priority.USER, readThrough = null)
    }

    /**
     * Reads the page before the oldest stored message of a loaded room.
     * Returns whether the start of the room has been reached.
     */
    suspend fun loadOlder(roomId: String): Boolean {
        val block = dao.timeline(roomId) ?: return true
        if (!block.hasOlder) return true
        val page = forRoom(roomId) {
            request(Priority.USER) { messages(it, roomId, cursor = block.oldest.toString(), after = null, limit = limits.history) }
        } ?: return true
        val messages = historyPage(roomId, block.oldest, limits.history, page)
        if (messages.isEmpty()) return true
        dao.applyOlder(roomId, messages)
        loadSenders(roomId)
        return dao.timeline(roomId)?.hasOlder != true
    }

    /** Marks the room as read up to its newest stored message. */
    suspend fun markRead(roomId: String) {
        dao.timeline(roomId)?.let { dao.markRead(roomId, it.newest) }
    }

    /** Runs a full sync now, after any sync under way; returns its failure. */
    suspend fun syncNow(): ApiException? = runSync()

    private suspend fun runSync(): ApiException? = syncLock.withLock {
        mutableStatus.value = SyncStatus(running = true, failure = null)
        val failure = try {
            sync()
            null
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            e
        } catch (e: Exception) {
            // A database failure must not end the sync loop; the next
            // request tries again.
            ApiException(0, ApiException.LOCAL, "local failure", cause = e)
        }
        mutableStatus.value = SyncStatus(running = false, failure = failure)
        if (failure?.status == 429) {
            scope.launch {
                delay(pausedUntil - clock())
                requestSync()
            }
        }
        failure
    }

    private suspend fun sync() {
        val listed = listLock.withLock {
            val (rooms, complete) = listRooms()
            val entities = rooms.map { it.toEntity(me, activity = 0) }
            dao.storeRoomList(entities, complete)
            entities
        }
        loadProfiles(listed.mapNotNull { it.peerId })

        val timelines = dao.timelines().associateBy { it.roomId }
        val stale = listed.filter { room ->
            timelines[room.roomId] != null && room.lastMessageAt != null &&
                room.lastMessageAt > (dao.newestMessageTime(room.roomId) ?: Long.MIN_VALUE)
        }
        val unloaded = listed.filter { timelines[it.roomId] == null }.take(limits.preloadedRooms)
        // A few rooms at a time, so that the others do not hold their locks
        // while they wait for the budget.
        val running = Semaphore(CONCURRENT_ROOMS)
        coroutineScope {
            (stale + unloaded).forEach { room ->
                launch { running.withPermit { refreshRoom(room.roomId, Priority.BACKGROUND, readThrough = null) } }
            }
        }
    }

    /**
     * Reads the room list. A listing over several pages reads the first page
     * again at the end: rooms with new activity move to the top and are not
     * repeated on later pages. Returns the rooms and whether the list is
     * complete.
     */
    private suspend fun listRooms(): Pair<List<RoomDto>, Boolean> {
        val rooms = LinkedHashMap<String, RoomDto>()
        var cursor: String? = null
        var pages = 0
        do {
            val page = request(Priority.BACKGROUND) { rooms(it, cursor, limits.roomPage) }
            if (page.rooms.size > limits.roomPage) throw invalid("room page over its limit")
            page.rooms.forEach { rooms.putIfAbsent(it.roomId, it) }
            cursor = page.nextCursor.ifEmpty { null }
            pages++
        } while (cursor != null && pages < limits.roomPages)
        if (pages == 1) return rooms.values.toList() to true
        val first = request(Priority.BACKGROUND) { rooms(it, null, limits.roomPage) }.rooms
        val moved = first.associateBy { it.roomId }
        val merged = first + rooms.values.filter { it.roomId !in moved }
        return merged to (cursor == null)
    }

    /** Applies one live event. */
    private suspend fun apply(frame: Frame) {
        when (frame) {
            is Frame.MessageCreated -> {
                val message = try {
                    frame.message.toEntity(frame.roomId)
                } catch (e: ApiException) {
                    return
                }
                // A room loaded because of this message shows it as unread.
                val readThrough = message.sequence - 1
                when (dao.applyFollowing(frame.roomId, listOf(message), live = true)) {
                    ChatDao.Following.APPLIED -> loadProfiles(listOf(message.senderId))
                    ChatDao.Following.GAP, ChatDao.Following.NOT_LOADED -> scheduleRefresh(frame.roomId, readThrough)
                    ChatDao.Following.UNKNOWN_ROOM -> addRoom(frame.roomId, readThrough)
                }
            }
            is Frame.MemberAdded -> if (frame.userId == me) addRoom(frame.roomId, readThrough = null)
            is Frame.MemberGone -> if (frame.userId == me) {
                departures.merge(frame.roomId, 1L, Long::plus)
                listLock.withLock { dao.deleteRooms(listOf(frame.roomId)) }
            }
            is Frame.Ready -> Unit
        }
    }

    /**
     * Lists a room the user has just joined, then loads its messages, in the
     * background. [readThrough] is the position up to which its messages
     * count as read; without one, a new dialog is unread and a channel's
     * history is not. Events for the room while it is being added lower it
     * and make the attempt run once more after this one, so that a user who
     * left and joined again meanwhile still gets the room.
     */
    private fun addRoom(roomId: String, readThrough: Long?) {
        val add = synchronized(adding) {
            adding[roomId]?.let { queued ->
                queued.readThrough = listOfNotNull(queued.readThrough, readThrough).minOrNull()
                queued.again = true
                return
            }
            PendingAdd(readThrough).also { adding[roomId] = it }
        }
        scope.launch {
            try {
                while (true) {
                    tryAddRoom(roomId, add)
                    // Deciding to stop and leaving the map happen together, so
                    // that no event falls between them.
                    synchronized(adding) {
                        if (!add.again) {
                            adding.remove(roomId)
                            return@launch
                        }
                        add.again = false
                    }
                }
            } catch (e: Throwable) {
                synchronized(adding) { adding.remove(roomId) }
                throw e
            }
        }
    }

    private suspend fun tryAddRoom(roomId: String, add: PendingAdd) {
        val departed = departures[roomId] ?: 0
        val room = if (dao.room(roomId) != null) {
            null
        } else {
            try {
                request(Priority.BACKGROUND) { room(it, roomId) }
            } catch (e: ApiException) {
                // 404: not a member any more. Otherwise the next sync lists it.
                return
            }
        }
        if (room != null) {
            val stored = listLock.withLock {
                // The user left while the room was read.
                if ((departures[roomId] ?: 0) != departed) return@withLock false
                if (dao.room(roomId) == null) dao.addRoom(room.toEntity(me, activity = 0))
                true
            }
            if (!stored) return
            room.participantIds?.filter { it != me }?.let { loadProfiles(it) }
        }
        val asked = synchronized(adding) { add.readThrough }
        val dialog = room?.kind == RoomEntity.DIRECT
        scheduleRefresh(roomId, listOfNotNull(asked, if (dialog) 0L else null).minOrNull())
    }

    /**
     * Refreshes a room after live events, in the background. Events that
     * arrive while a refresh is waiting or under way add one more refresh
     * after it, not one each. [readThrough] is what counts as read if the
     * room is loaded now for the first time; the lowest one asked for wins.
     */
    private fun scheduleRefresh(roomId: String, readThrough: Long?) {
        val refresh = synchronized(pending) {
            pending[roomId]?.let { queued ->
                queued.again = true
                queued.readThrough = listOfNotNull(queued.readThrough, readThrough).minOrNull()
                return
            }
            PendingRefresh(readThrough).also { pending[roomId] = it }
        }
        scope.launch {
            try {
                while (true) {
                    refreshRoom(roomId, Priority.BACKGROUND, synchronized(pending) { refresh.readThrough })
                    synchronized(pending) {
                        if (!refresh.again) {
                            pending.remove(roomId)
                            return@launch
                        }
                        refresh.again = false
                    }
                }
            } catch (e: Exception) {
                synchronized(pending) { pending.remove(roomId) }
                // The next sync retries.
                if (e is CancellationException) throw e
            }
        }
    }

    /** Loads or catches up a room in the background, one refresh per room at a time. */
    private suspend fun refreshRoom(roomId: String, priority: Priority, readThrough: Long?) {
        roomLocks.getOrPut(roomId) { Mutex() }.withLock { loadOrCatchUp(roomId, priority, readThrough) }
    }

    private suspend fun loadOrCatchUp(roomId: String, priority: Priority, readThrough: Long?) {
        if (dao.timeline(roomId) == null) loadLatest(roomId, priority, readThrough) else catchUp(roomId, priority)
    }

    private suspend fun loadLatest(roomId: String, priority: Priority, readThrough: Long?) {
        val page = forRoom(roomId) {
            request(priority) { messages(it, roomId, cursor = null, after = null, limit = limits.history) }
        } ?: return
        dao.applyLatest(roomId, historyPage(roomId, null, limits.history, page), readThrough)
        loadSenders(roomId)
    }

    /**
     * Reads every message after the stored block. A room that is too far
     * behind, or whose catch-up does not match the API, is reset to its
     * latest page instead.
     */
    private suspend fun catchUp(roomId: String, priority: Priority) {
        repeat(limits.catchUpPages) {
            val block = dao.timeline(roomId) ?: return loadLatest(roomId, priority, readThrough = null)
            val page = forRoom(roomId) {
                request(priority) { messages(it, roomId, cursor = null, after = block.newest.toString(), limit = limits.catchUp) }
            } ?: return
            val (messages, more) = try {
                catchUpPage(roomId, block.newest, limits.catchUp, page)
            } catch (e: ApiException) {
                return loadLatest(roomId, priority, readThrough = null)
            }
            when (dao.applyFollowing(roomId, messages, live = false)) {
                ChatDao.Following.UNKNOWN_ROOM -> return
                ChatDao.Following.NOT_LOADED -> return loadLatest(roomId, priority, readThrough = null)
                ChatDao.Following.GAP -> Unit
                ChatDao.Following.APPLIED -> if (!more) return loadSenders(roomId)
            }
        }
        loadLatest(roomId, priority, readThrough = null)
    }

    /**
     * Runs a request for one room. A 404 means the user is no longer a
     * member: the room is deleted and null returned.
     */
    private suspend fun <T> forRoom(roomId: String, request: suspend () -> T): T? = try {
        request()
    } catch (e: ApiException) {
        if (e.status != 404) throw e
        listLock.withLock { dao.deleteRooms(listOf(roomId)) }
        null
    }

    private suspend fun loadSenders(roomId: String) {
        loadProfiles(dao.unknownSenders(roomId))
    }

    /**
     * Reads the profiles that are not stored yet, in the background. An
     * account that no longer exists is stored without a name; other failures
     * are retried the next time the profile is needed.
     */
    private suspend fun loadProfiles(userIds: List<String>) {
        val wanted = userIds.distinct().filter { it != me && isUuid(it) }
        if (wanted.isEmpty()) return
        val known = wanted.chunked(500).flatMap { dao.knownProfiles(it) }.toSet()
        for (userId in wanted) {
            if (userId in known || !profilesInFlight.add(userId)) continue
            scope.launch {
                try {
                    val profile = try {
                        request(Priority.PROFILE) { user(it, userId) }.let { ProfileEntity(userId, it.username) }
                    } catch (e: ApiException) {
                        if (e.status != 404) return@launch
                        ProfileEntity(userId, null)
                    }
                    dao.upsertProfiles(listOf(profile))
                } finally {
                    profilesInFlight.remove(userId)
                }
            }
        }
    }

    /** Sends a request within the budget; a 429 pauses every request. */
    private suspend fun <T : Any> request(
        priority: Priority,
        call: suspend GatewayApi.(authorization: String) -> Response<T>,
    ): T {
        when (priority) {
            Priority.USER -> rate.takeNow()
            Priority.BACKGROUND -> rate.take()
            Priority.PROFILE -> rate.take(reserve = limits.profileReserve)
        }
        return connections.withPermit {
            val wait = pausedUntil - clock()
            if (wait > 0) delay(wait)
            try {
                requests.send(call)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException) {
                if (e.status == 429) {
                    pausedUntil = clock() + (e.retryAfterSeconds ?: DEFAULT_RETRY_AFTER_SECONDS) * 1000
                }
                throw e
            }
        }
    }

    companion object {
        const val CONCURRENT_REQUESTS = 2

        /** Rooms a sync loads or catches up at the same time. */
        const val CONCURRENT_ROOMS = 2
        const val EVENT_BUFFER = 1024
        private const val DEFAULT_RETRY_AFTER_SECONDS = 30L
    }
}
