package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ProfileEntity
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
 * event adds while a listing is under way is not deleted as unlisted. Each
 * room loads or catches up under its own lock; older pages need none, since
 * the database drops a page that no longer fits. All requests share a
 * budget of [CONCURRENT_REQUESTS] and pause together after a 429.
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
    )

    private val listLock = Mutex()
    private val syncLock = Mutex()
    private val roomLocks = ConcurrentHashMap<String, Mutex>()
    private val budget = Semaphore(CONCURRENT_REQUESTS)
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

    /** Loads a room that is being opened, or catches it up. */
    suspend fun openRoom(roomId: String) {
        refreshRoom(roomId)
    }

    /**
     * Reads the page before the oldest stored message of a loaded room.
     * Returns whether the start of the room has been reached.
     */
    suspend fun loadOlder(roomId: String): Boolean {
        val block = dao.timeline(roomId) ?: return true
        if (!block.hasOlder) return true
        val page = forRoom(roomId) {
            request { messages(it, roomId, cursor = block.oldest.toString(), after = null, limit = limits.history) }
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
        // The budget bounds how many of them run at once.
        coroutineScope {
            (stale + unloaded).forEach { room -> launch { refreshRoom(room.roomId) } }
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
            val page = request { rooms(it, cursor, limits.roomPage) }
            if (page.rooms.size > limits.roomPage) throw invalid("room page over its limit")
            page.rooms.forEach { rooms.putIfAbsent(it.roomId, it) }
            cursor = page.nextCursor.ifEmpty { null }
            pages++
        } while (cursor != null && pages < limits.roomPages)
        if (pages == 1) return rooms.values.toList() to true
        val first = request { rooms(it, null, limits.roomPage) }.rooms
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
                when (dao.applyFollowing(frame.roomId, listOf(message), live = true)) {
                    ChatDao.Following.APPLIED -> loadProfiles(listOf(message.senderId))
                    ChatDao.Following.GAP, ChatDao.Following.NOT_LOADED -> background { refreshRoom(frame.roomId) }
                    ChatDao.Following.UNKNOWN_ROOM -> addRoom(frame.roomId)
                }
            }
            is Frame.MemberAdded -> if (frame.userId == me) addRoom(frame.roomId)
            is Frame.MemberGone -> if (frame.userId == me) listLock.withLock { dao.deleteRooms(listOf(frame.roomId)) }
            is Frame.Ready -> Unit
        }
    }

    /** Lists a room the user has just joined, then loads its messages. */
    private suspend fun addRoom(roomId: String) {
        val added = try {
            listLock.withLock {
                if (dao.room(roomId) != null) return@withLock true
                // Not forRoom: its 404 handling takes listLock, which is held here.
                val room = try {
                    request { room(it, roomId) }
                } catch (e: ApiException) {
                    if (e.status == 404) return@withLock false
                    throw e
                }
                dao.addRoom(room.toEntity(me, activity = 0))
                room.participantIds?.filter { it != me }?.let { loadProfiles(it) }
                true
            }
        } catch (e: ApiException) {
            // Unreachable for now: the next sync lists the room.
            false
        }
        if (added) background { refreshRoom(roomId) }
    }

    /** Runs room work after an event, where nobody waits for its failure. */
    private fun background(work: suspend () -> Unit) {
        scope.launch {
            try {
                work()
            } catch (e: ApiException) {
                // The next sync retries.
            }
        }
    }

    private suspend fun refreshRoom(roomId: String) {
        roomLocks.getOrPut(roomId) { Mutex() }.withLock {
            if (dao.timeline(roomId) == null) loadLatest(roomId) else catchUp(roomId)
        }
    }

    private suspend fun loadLatest(roomId: String) {
        val page = forRoom(roomId) {
            request { messages(it, roomId, cursor = null, after = null, limit = limits.history) }
        } ?: return
        dao.applyLatest(roomId, historyPage(roomId, null, limits.history, page))
        loadSenders(roomId)
    }

    /**
     * Reads every message after the stored block. A room that is too far
     * behind, or whose catch-up does not match the API, is reset to its
     * latest page instead.
     */
    private suspend fun catchUp(roomId: String) {
        repeat(limits.catchUpPages) {
            val block = dao.timeline(roomId) ?: return loadLatest(roomId)
            val page = forRoom(roomId) {
                request { messages(it, roomId, cursor = null, after = block.newest.toString(), limit = limits.catchUp) }
            } ?: return
            val (messages, more) = try {
                catchUpPage(roomId, block.newest, limits.catchUp, page)
            } catch (e: ApiException) {
                return loadLatest(roomId)
            }
            when (dao.applyFollowing(roomId, messages, live = false)) {
                ChatDao.Following.UNKNOWN_ROOM -> return
                ChatDao.Following.NOT_LOADED -> return loadLatest(roomId)
                ChatDao.Following.GAP -> Unit
                ChatDao.Following.APPLIED -> if (!more) return loadSenders(roomId)
            }
        }
        loadLatest(roomId)
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
                        request { user(it, userId) }.let { ProfileEntity(userId, it.username) }
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
    private suspend fun <T : Any> request(call: suspend GatewayApi.(authorization: String) -> Response<T>): T =
        budget.withPermit {
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

    companion object {
        const val CONCURRENT_REQUESTS = 2
        const val EVENT_BUFFER = 1024
        private const val DEFAULT_RETRY_AFTER_SECONDS = 30L
    }
}
