package io.github.alexandrkhromov2005.vaultchat.core.chat.db

import androidx.paging.PagingSource
import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.Transaction
import androidx.room.Upsert
import io.github.alexandrkhromov2005.vaultchat.core.chat.Timeline
import kotlinx.coroutines.flow.Flow

/**
 * Reads and writes of the chat database. Every change to a room's messages
 * goes through one transaction that reads the stored block, asks [Timeline]
 * what to do and writes the result, so that concurrent pages and live
 * messages cannot break the block.
 */
@Dao
abstract class ChatDao {

    @Query(ROOM_LIST + " ORDER BY r.activity DESC, r.room_id DESC")
    abstract fun roomList(me: String): Flow<List<RoomListItem>>

    @Query(ROOM_LIST + " WHERE r.room_id = :roomId")
    abstract fun roomItem(me: String, roomId: String): Flow<RoomListItem?>

    @Query(
        "SELECT m.*, p.username AS sender_name, (p.user_id IS NOT NULL) AS sender_known FROM messages m " +
            "LEFT JOIN profiles p ON p.user_id = m.sender_id WHERE m.room_id = :roomId ORDER BY m.sequence DESC",
    )
    abstract fun messages(roomId: String): PagingSource<Int, MessageItem>

    @Query("SELECT * FROM timelines WHERE room_id = :roomId")
    abstract suspend fun timeline(roomId: String): TimelineEntity?

    @Query("SELECT * FROM timelines WHERE room_id = :roomId")
    abstract fun timelineFlow(roomId: String): Flow<TimelineEntity?>

    @Query("SELECT * FROM rooms WHERE room_id = :roomId")
    abstract suspend fun room(roomId: String): RoomEntity?

    @Query("SELECT * FROM rooms ORDER BY activity DESC, room_id DESC")
    abstract suspend fun rooms(): List<RoomEntity>

    @Query("SELECT * FROM timelines")
    abstract suspend fun timelines(): List<TimelineEntity>

    @Query("SELECT * FROM messages WHERE room_id = :roomId ORDER BY sequence")
    abstract suspend fun storedMessages(roomId: String): List<MessageEntity>

    @Query("SELECT MAX(created_at) FROM messages WHERE room_id = :roomId")
    abstract suspend fun newestMessageTime(roomId: String): Long?

    @Query("SELECT user_id FROM profiles WHERE user_id IN (:userIds)")
    abstract suspend fun knownProfiles(userIds: List<String>): List<String>

    @Query(
        "SELECT DISTINCT m.sender_id FROM messages m LEFT JOIN profiles p ON p.user_id = m.sender_id " +
            "WHERE m.room_id = :roomId AND p.user_id IS NULL",
    )
    abstract suspend fun unknownSenders(roomId: String): List<String>

    @Upsert
    abstract suspend fun upsertProfiles(profiles: List<ProfileEntity>)

    @Query("UPDATE timelines SET last_read = :sequence WHERE room_id = :roomId AND last_read < :sequence")
    abstract suspend fun markRead(roomId: String, sequence: Long)

    @Query("SELECT COALESCE(MAX(activity), 0) FROM rooms")
    protected abstract suspend fun maxActivity(): Long

    @Query("SELECT room_id FROM rooms")
    protected abstract suspend fun roomIds(): List<String>

    @Upsert
    protected abstract suspend fun upsertRooms(rooms: List<RoomEntity>)

    @Upsert
    protected abstract suspend fun upsertTimeline(timeline: TimelineEntity)

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    protected abstract suspend fun insertMessages(messages: List<MessageEntity>)

    @Query("DELETE FROM messages WHERE room_id = :roomId")
    protected abstract suspend fun deleteMessages(roomId: String)

    @Query("DELETE FROM rooms WHERE room_id IN (:roomIds)")
    protected abstract suspend fun deleteRoomBatch(roomIds: List<String>)

    @Query("UPDATE rooms SET activity = :activity, last_message_at = MAX(COALESCE(last_message_at, 0), :at) WHERE room_id = :roomId")
    protected abstract suspend fun bump(roomId: String, activity: Long, at: Long)

    /**
     * Stores a room listing in its order, above every stored room; their
     * activity is assigned here. With [complete], rooms that are not listed
     * are deleted with their messages; returns their IDs.
     */
    @Transaction
    open suspend fun storeRoomList(rooms: List<RoomEntity>, complete: Boolean): List<String> {
        val before = roomIds()
        val base = maxActivity() + rooms.size + 1
        val stored = rooms.mapIndexed { index, room -> room.copy(activity = base - index) }
        upsertRooms(stored)
        if (!complete) return emptyList()
        val listed = stored.mapTo(HashSet()) { it.roomId }
        val removed = before.filter { it !in listed }
        deleteRooms(removed)
        return removed
    }

    /** Adds a single room at the top of the list, keeping its stored timeline. */
    @Transaction
    open suspend fun addRoom(room: RoomEntity) {
        upsertRooms(listOf(room.copy(activity = maxActivity() + 1)))
    }

    open suspend fun deleteRooms(roomIds: List<String>) {
        // SQLite limits the number of bound parameters.
        roomIds.chunked(500).forEach { deleteRoomBatch(it) }
    }

    /** Applies the latest history page; false if the room is not stored. */
    @Transaction
    open suspend fun applyLatest(roomId: String, page: List<MessageEntity>): Boolean {
        if (room(roomId) == null) return false
        Timeline.latest(roomId, timeline(roomId), page)?.let { write(roomId, it) }
        return true
    }

    /** The result of [applyFollowing]. */
    enum class Following { APPLIED, GAP, NOT_LOADED, UNKNOWN_ROOM }

    /**
     * Applies messages that follow the stored block, oldest first. With
     * [live], the room also moves to the top of the list, since it has new
     * activity whether or not the messages could be stored.
     */
    @Transaction
    open suspend fun applyFollowing(roomId: String, messages: List<MessageEntity>, live: Boolean): Following {
        if (room(roomId) == null) return Following.UNKNOWN_ROOM
        if (live && messages.isNotEmpty()) bump(roomId, maxActivity() + 1, messages.maxOf { it.createdAt })
        val block = timeline(roomId) ?: return Following.NOT_LOADED
        val extension = Timeline.extend(block, messages)
        extension.update?.let { write(roomId, it) }
        return if (extension.gap) Following.GAP else Following.APPLIED
    }

    /** Applies an older history page; false if it no longer fits the block. */
    @Transaction
    open suspend fun applyOlder(roomId: String, page: List<MessageEntity>): Boolean {
        val update = Timeline.older(timeline(roomId), page) ?: return false
        write(roomId, update)
        return true
    }

    private suspend fun write(roomId: String, update: Timeline.Update) {
        if (update.replace) deleteMessages(roomId)
        upsertTimeline(update.timeline)
        insertMessages(update.insert)
    }

    private companion object {
        const val ROOM_LIST = "SELECT r.*, p.username AS peer_name, m.text AS last_text, m.sender_id AS last_sender_id, " +
            "m.attachments AS last_attachments, m.created_at AS last_created_at, " +
            "(SELECT COUNT(*) FROM messages u WHERE u.room_id = r.room_id AND u.sequence > t.last_read " +
            "AND u.sender_id != :me) AS unread " +
            "FROM rooms r LEFT JOIN timelines t ON t.room_id = r.room_id " +
            "LEFT JOIN messages m ON m.room_id = r.room_id AND m.sequence = t.newest " +
            "LEFT JOIN profiles p ON p.user_id = r.peer_id"
    }
}
