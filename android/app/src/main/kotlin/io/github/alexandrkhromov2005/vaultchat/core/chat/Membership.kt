package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatSync.Priority
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MemberEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MemberItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ProfileEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RosterEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.CreateChannelRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.NotRepeatable
import io.github.alexandrkhromov2005.vaultchat.core.net.OpenDirectRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.PublicProfile
import io.github.alexandrkhromov2005.vaultchat.core.net.noContent
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.Flow

/**
 * Finding users, opening dialogs, creating channels and managing a
 * channel's members (docs/android/rooms.md). Requests go through [sync], so
 * they share its budget and 429 pause, at the priority of requests the user
 * waits for. Every room the server returns is checked before it is stored.
 */
class Membership(
    private val dao: ChatDao,
    private val me: String,
    private val sync: ChatSync,
    val limits: Limits = Limits(),
) {
    data class Limits(
        val searchPage: Int = 20,
        val memberPage: Int = 100,
        val memberPages: Int = 10,
        /** Reads of a roster that changed while it was read, after which the last one is kept. */
        val rosterAttempts: Int = 3,
    )

    /**
     * Users whose name starts with [query], which [UserQuery] must accept,
     * without the caller. Their names are stored for later.
     */
    suspend fun search(query: String): List<PublicProfile> {
        require(UserQuery.check(query) == null) { "a query the server rejects" }
        val page = sync.request(Priority.USER) { this.searchUsers(it, query, limits.searchPage) }
        if (page.users.size > limits.searchPage) throw invalid("search page over its limit")
        if (page.users.any { !isUuid(it.userId) }) throw invalid("user_id is not a UUID")
        // The results are listed by user ID, which must be unique.
        if (page.users.distinctBy { it.userId.lowercase() }.size != page.users.size) throw invalid("a user twice")
        val users = page.users.filter { !it.userId.equals(me, ignoreCase = true) }
        dao.upsertProfiles(users.map { ProfileEntity(it.userId, it.username) })
        return users
    }

    /** Opens the dialog with [peerId], creating it the first time, and returns its room ID. */
    suspend fun openDialog(peerId: String): String {
        val room = sync.request(Priority.USER) { this.openDirect(it, OpenDirectRequest(peerId)) }
        val participants = room.participantIds.orEmpty().map { it.lowercase() }.sorted()
        if (room.kind != RoomEntity.DIRECT || participants != listOf(me.lowercase(), peerId.lowercase()).sorted()) {
            throw invalid("not the dialog asked for")
        }
        val entity = room.toEntity(me, activity = 0)
        sync.storeJoined(entity)
        return entity.roomId
    }

    /**
     * Creates a channel named [name], which [ChannelName] must accept, and
     * returns its room ID. The request is not idempotent and is never
     * repeated; when its outcome is unknown ([isUnknownOutcome]) a sync is
     * started, which lists the channel if it was created.
     */
    suspend fun createChannel(name: String, private: Boolean): String {
        require(ChannelName.check(name) == null) { "a name the server rejects" }
        try {
            val room = sync.request(Priority.USER) { this.createChannel(it, CreateChannelRequest(name, private), NotRepeatable) }
            if (room.kind != RoomEntity.CHANNEL || !room.ownerId.equals(me, ignoreCase = true) || room.name != name ||
                room.private != private
            ) {
                throw invalid("not the channel asked for")
            }
            val entity = room.toEntity(me, activity = 0)
            try {
                sync.storeJoined(entity)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // The channel exists, but this device could not store it.
                throw ApiException(0, ApiException.LOCAL, "local failure", cause = e)
            }
            return entity.roomId
        } catch (e: ApiException) {
            if (isUnknownOutcome(e)) sync.requestSync()
            throw e
        }
    }

    /** Members of a room as stored, ordered by user ID; empty until its roster is read. */
    fun members(roomId: String): Flow<List<MemberItem>> = dao.members(roomId)

    /** Whether the stored roster of a room is complete; null until it is read. */
    fun roster(roomId: String): Flow<RosterEntity?> = dao.rosterFlow(roomId)

    /**
     * Reads the roster of a room and replaces the stored one. A roster that
     * changed while it was read is read again, up to [Limits.rosterAttempts]
     * times. A 404 deletes the room.
     */
    suspend fun loadRoster(roomId: String) {
        repeat(limits.rosterAttempts) {
            val version = sync.rosterVersion(roomId)
            val (members, complete) = readRoster(roomId) ?: return
            if (!dao.storeRoster(roomId, members, complete)) return
            if (sync.rosterVersion(roomId) == version) return
        }
    }

    private suspend fun readRoster(roomId: String): Pair<List<MemberEntity>, Boolean>? {
        val members = mutableListOf<MemberEntity>()
        var cursor: String? = null
        var pages = 0
        do {
            // Only the first page is what the user waits for.
            val priority = if (pages == 0) Priority.USER else Priority.BACKGROUND
            val after = cursor
            val page = sync.forRoom(roomId) {
                sync.request(priority) { this.members(it, roomId, after, limits.memberPage) }
            } ?: return null
            val (read, next) = memberPage(roomId, after, limits.memberPage, page)
            members += read
            cursor = next
            pages++
        } while (cursor != null && pages < limits.memberPages)
        return members to (cursor == null)
    }

    /**
     * Adds [userId] to a channel the user owns. A 404 means that the user or
     * the room is gone: the room is read again, which deletes it if it is.
     */
    suspend fun addMember(roomId: String, userId: String) {
        try {
            sync.request(Priority.USER) { this.addMember(it, roomId, userId).noContent() }
        } catch (e: ApiException) {
            if (e.status == 404) {
                try {
                    sync.forRoom(roomId) { sync.request(Priority.USER) { this.room(it, roomId) } }
                } catch (check: ApiException) {
                    // Unknown for now; the next sync settles the room.
                }
            }
            throw e
        }
        sync.rosterChanged(roomId)
        dao.addMember(roomId, userId)
    }

    /** Removes [userId] from a channel the user owns. A 404 means the user lost access: the room is deleted. */
    suspend fun removeMember(roomId: String, userId: String) {
        try {
            sync.request(Priority.USER) { this.removeMember(it, roomId, userId).noContent() }
        } catch (e: ApiException) {
            if (e.status == 404) sync.forget(roomId)
            throw e
        }
        sync.rosterChanged(roomId)
        dao.removeMember(roomId, userId)
    }

    /**
     * Leaves a channel; its messages and unsent queue are deleted. A 404 is
     * the same outcome: the user is not a member, for instance after a
     * repeat of a leave whose answer was lost.
     */
    suspend fun leave(roomId: String) {
        try {
            sync.request(Priority.USER) { this.leave(it, roomId).noContent() }
        } catch (e: ApiException) {
            if (e.status != 404) throw e
        }
        sync.forget(roomId)
    }

    companion object {
        /**
         * Whether a failed request may still have been carried out: the
         * connection broke, the answer was not what the API describes, the
         * Gateway or a service behind it failed or timed out, or this device
         * could not store the answer.
         */
        fun isUnknownOutcome(e: ApiException): Boolean =
            e.status == 0 || e.code == ApiException.INVALID_RESPONSE || e.status >= 500 || e.status == 408
    }
}
