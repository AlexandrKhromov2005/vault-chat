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

    @Query("UPDATE rooms SET activity = :activity WHERE room_id = :roomId")
    protected abstract suspend fun setActivity(roomId: String, activity: Long)

    // The outgoing queue (docs/android/send.md).

    @Query("SELECT * FROM outbox WHERE room_id = :roomId ORDER BY position")
    abstract fun outgoing(roomId: String): Flow<List<OutgoingEntity>>

    @Query("SELECT * FROM outbox WHERE client_message_id = :clientMessageId")
    abstract suspend fun outgoingRow(clientMessageId: String): OutgoingEntity?

    @Query("SELECT * FROM outbox WHERE room_id = :roomId ORDER BY position")
    abstract suspend fun storedOutgoing(roomId: String): List<OutgoingEntity>

    /** Rooms with a message waiting to be sent, the longest waiting first. */
    @Query("SELECT room_id FROM outbox WHERE state = 'queued' GROUP BY room_id ORDER BY MIN(position)")
    abstract suspend fun queuedRooms(): List<String>

    /** The next message of a room to send: the oldest queued one. */
    @Query("SELECT * FROM outbox WHERE room_id = :roomId AND state = 'queued' ORDER BY position LIMIT 1")
    abstract suspend fun nextQueued(roomId: String): OutgoingEntity?

    @Query("SELECT COUNT(*) FROM outbox WHERE state = 'queued'")
    abstract fun queuedCount(): Flow<Int>

    /** Messages the server has not stored, as far as this device knows. */
    @Query("SELECT COUNT(*) FROM outbox WHERE state != 'sent'")
    abstract fun unsentCount(): Flow<Int>

    /** Rooms with messages the server has not stored; they exist only on this device. */
    @Query("SELECT DISTINCT room_id FROM outbox WHERE state != 'sent'")
    abstract suspend fun roomsWithUnsent(): List<String>

    @Query("SELECT COALESCE(MAX(position), 0) FROM outbox")
    protected abstract suspend fun maxPosition(): Long

    @Insert
    protected abstract suspend fun insertOutgoing(row: OutgoingEntity)

    @Query("DELETE FROM outbox WHERE client_message_id = :clientMessageId")
    protected abstract suspend fun deleteOutgoing(clientMessageId: String)

    @Query("UPDATE outbox SET state = 'sent', sequence = :sequence, error = NULL WHERE client_message_id = :clientMessageId AND state = 'queued'")
    protected abstract suspend fun setSent(clientMessageId: String, sequence: Long)

    @Query("UPDATE outbox SET state = 'failed', error = :error WHERE client_message_id = :clientMessageId AND state = 'queued'")
    abstract suspend fun markFailed(clientMessageId: String, error: String)

    @Query("DELETE FROM outbox WHERE client_message_id = :clientMessageId AND state = 'failed'")
    abstract suspend fun deleteFailed(clientMessageId: String)

    /**
     * Deletes the rows of a room that its stored block now shows: those that a
     * stored message of their sender matches by key and text, and sent rows
     * within or below the block. The key alone is not enough: keys are per
     * sender, and another member may use the same one; and a stored message
     * with the same key but another text (the server's 409) shows only that
     * this text was not stored.
     */
    @Query(
        "DELETE FROM outbox WHERE room_id = :roomId AND (" +
            "EXISTS (SELECT 1 FROM messages m WHERE m.room_id = outbox.room_id " +
            "AND m.client_message_id = outbox.client_message_id AND m.sender_id = outbox.sender_id " +
            "AND m.text = outbox.text) " +
            "OR (state = 'sent' AND sequence <= (SELECT newest FROM timelines t WHERE t.room_id = outbox.room_id)))",
    )
    protected abstract suspend fun resolveOutgoing(roomId: String)

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
        // A listing may miss a room that moved meanwhile; a room with unsent
        // messages is deleted only once the server confirms it (ChatSync).
        val unsent = roomsWithUnsent().toSet()
        val removed = before.filter { it !in listed && it !in unsent }
        deleteRooms(removed)
        return removed
    }

    /** Adds a single room at the top of the list, keeping its stored timeline. */
    @Transaction
    open suspend fun addRoom(room: RoomEntity) {
        upsertRooms(listOf(room.copy(activity = maxActivity() + 1)))
    }

    /**
     * Queues a message at the end of the queue and moves its room to the top
     * of the list; false if the room is not stored.
     */
    @Transaction
    open suspend fun enqueue(row: OutgoingEntity): Boolean {
        if (room(row.roomId) == null) return false
        insertOutgoing(
            row.copy(
                position = maxPosition() + 1,
                state = OutgoingEntity.QUEUED,
                sequence = null,
                error = null,
                afterSequence = timeline(row.roomId)?.newest ?: 0,
            ),
        )
        setActivity(row.roomId, maxActivity() + 1)
        return true
    }

    /** Records that the server stored a queued message as [sequence]. */
    @Transaction
    open suspend fun markSent(roomId: String, clientMessageId: String, sequence: Long) {
        setSent(clientMessageId, sequence)
        resolveOutgoing(roomId)
    }

    /**
     * Queues a failed message again at the end of the queue, under
     * [clientMessageId] (its own key, or a new one after a conflict).
     * Returns false if the row is not failed any more.
     */
    @Transaction
    open suspend fun requeue(failedId: String, clientMessageId: String): Boolean {
        val row = outgoingRow(failedId)?.takeIf { it.state == OutgoingEntity.FAILED } ?: return false
        deleteOutgoing(failedId)
        insertOutgoing(
            row.copy(
                clientMessageId = clientMessageId,
                position = maxPosition() + 1,
                state = OutgoingEntity.QUEUED,
                error = null,
                afterSequence = timeline(row.roomId)?.newest ?: 0,
            ),
        )
        return true
    }

    open suspend fun deleteRooms(roomIds: List<String>) {
        // SQLite limits the number of bound parameters.
        roomIds.chunked(500).forEach { deleteRoomBatch(it) }
    }

    /**
     * Applies the latest history page; false if the room is not stored.
     * [readThrough] is what counts as read on a first load (see Timeline.latest).
     */
    @Transaction
    open suspend fun applyLatest(roomId: String, page: List<MessageEntity>, readThrough: Long? = null): Boolean {
        if (room(roomId) == null) return false
        Timeline.latest(roomId, timeline(roomId), page, readThrough)?.let { write(roomId, it) }
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
        resolveOutgoing(roomId)
    }

    private companion object {
        // The room's preview from the outbox: its last row, if it is queued or
        // sent, or failed with no message stored after it was queued. Local and
        // server clocks differ, so sequences decide, not times.
        const val PREVIEWED = "o.room_id = r.room_id AND (o.state != 'failed' OR o.after_sequence >= COALESCE(t.newest, 0)) " +
            "ORDER BY o.position DESC LIMIT 1"

        const val ROOM_LIST = "SELECT r.*, p.username AS peer_name, m.text AS last_text, m.sender_id AS last_sender_id, " +
            "m.attachments AS last_attachments, m.created_at AS last_created_at, " +
            "(SELECT COUNT(*) FROM messages u WHERE u.room_id = r.room_id AND u.sequence > t.last_read " +
            "AND u.sender_id != :me) AS unread, " +
            "COALESCE(t.oldest > t.last_read + 1, 0) AS unread_more, " +
            "(SELECT o.text FROM outbox o WHERE " + PREVIEWED + ") AS pending_text, " +
            "(SELECT o.state FROM outbox o WHERE " + PREVIEWED + ") AS pending_state, " +
            "(SELECT o.created_at FROM outbox o WHERE " + PREVIEWED + ") AS pending_created_at " +
            "FROM rooms r LEFT JOIN timelines t ON t.room_id = r.room_id " +
            "LEFT JOIN messages m ON m.room_id = r.room_id AND m.sequence = t.newest " +
            "LEFT JOIN profiles p ON p.user_id = r.peer_id"
    }
}
