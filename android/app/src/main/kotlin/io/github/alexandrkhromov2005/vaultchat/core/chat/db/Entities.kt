package io.github.alexandrkhromov2005.vaultchat.core.chat.db

import androidx.room.ColumnInfo
import androidx.room.Embedded
import androidx.room.Entity
import androidx.room.ForeignKey
import androidx.room.Index
import androidx.room.PrimaryKey

/**
 * A room the user is a member of. [activity] orders the room list: rooms
 * keep the order of the latest listing, and a room with a new message moves
 * above all others. Times here and in the other tables are microseconds
 * since the epoch, the precision of the server.
 */
@Entity(tableName = "rooms")
data class RoomEntity(
    @PrimaryKey @ColumnInfo(name = "room_id") val roomId: String,
    val kind: String,
    val name: String?,
    @ColumnInfo(name = "owner_id") val ownerId: String?,
    @ColumnInfo(name = "is_private") val isPrivate: Boolean,
    @ColumnInfo(name = "created_at") val createdAt: Long,
    /** The other participant of a direct dialog. */
    @ColumnInfo(name = "peer_id") val peerId: String?,
    @ColumnInfo(name = "last_message_at") val lastMessageAt: Long?,
    val activity: Long,
) {
    companion object {
        const val DIRECT = "direct"
        const val CHANNEL = "channel"
    }
}

/**
 * The stored block of a loaded room: every message with a sequence in
 * [oldest]..[newest] is stored, and no other. An empty room has newest 0 and
 * oldest 1. A room without a row has not been loaded yet. [lastRead] is the
 * newest sequence shown on this device.
 */
@Entity(
    tableName = "timelines",
    foreignKeys = [ForeignKey(RoomEntity::class, ["room_id"], ["room_id"], onDelete = ForeignKey.CASCADE)],
)
data class TimelineEntity(
    @PrimaryKey @ColumnInfo(name = "room_id") val roomId: String,
    val newest: Long,
    val oldest: Long,
    @ColumnInfo(name = "last_read") val lastRead: Long,
) {
    val hasOlder: Boolean get() = oldest > 1
}

@Entity(
    tableName = "messages",
    primaryKeys = ["room_id", "sequence"],
    foreignKeys = [ForeignKey(RoomEntity::class, ["room_id"], ["room_id"], onDelete = ForeignKey.CASCADE)],
    indices = [Index("message_id")],
)
data class MessageEntity(
    @ColumnInfo(name = "room_id") val roomId: String,
    val sequence: Long,
    @ColumnInfo(name = "message_id") val messageId: String,
    @ColumnInfo(name = "sender_id") val senderId: String,
    @ColumnInfo(name = "client_message_id") val clientMessageId: String,
    val text: String,
    /** Attachment IDs separated by commas; empty without attachments. */
    val attachments: String,
    @ColumnInfo(name = "created_at") val createdAt: Long,
) {
    val attachmentCount: Int get() = if (attachments.isEmpty()) 0 else attachments.count { it == ',' } + 1

    val attachmentIds: List<String> get() = if (attachments.isEmpty()) emptyList() else attachments.split(',')
}

/**
 * A message of this user that is not yet part of its room's stored block
 * (docs/android/send.md). [position] orders the queue; [clientMessageId] is
 * the retry key and never changes while the row is retried, except after a
 * conflict. A row is deleted once a stored message of [senderId] with the same
 * key is in the room, or once the block reaches a [SENT] row's [sequence].
 */
@Entity(
    tableName = "outbox",
    foreignKeys = [ForeignKey(RoomEntity::class, ["room_id"], ["room_id"], onDelete = ForeignKey.CASCADE)],
    indices = [Index("room_id", "position"), Index("position")],
)
data class OutgoingEntity(
    @PrimaryKey @ColumnInfo(name = "client_message_id") val clientMessageId: String,
    @ColumnInfo(name = "room_id") val roomId: String,
    @ColumnInfo(name = "sender_id") val senderId: String,
    val position: Long,
    val text: String,
    /** Local time of queuing, microseconds since the epoch like the server's times. */
    @ColumnInfo(name = "created_at") val createdAt: Long,
    val state: String,
    /** The sequence the server gave it; set in [SENT]. */
    val sequence: Long? = null,
    /** Why the server rejected it; set in [FAILED]. */
    val error: String? = null,
    /** The newest stored sequence of the room when it was queued, to place a failed row in the room list. */
    @ColumnInfo(name = "after_sequence") val afterSequence: Long = 0,
) {
    companion object {
        /** Waiting to be sent, or being sent. */
        const val QUEUED = "queued"

        /** Stored by the server, but the room's block does not reach it yet. */
        const val SENT = "sent"

        /** Rejected by the server; sent again only when the user asks. */
        const val FAILED = "failed"

        const val ERROR_INVALID = "invalid"
        const val ERROR_NO_ACCESS = "no_access"
        const val ERROR_CONFLICT = "conflict"

        /** An upload whose outcome is unknown (docs/android/attachments.md). */
        const val ERROR_UPLOAD = "upload"

        /** The server did not accept a file. */
        const val ERROR_FILE = "file"

        /** A file's upload kept running out of time before all of it had left; nothing is stored. */
        const val ERROR_TOO_SLOW = "too_slow"
    }
}

/**
 * A file of a queued message (docs/android/attachments.md), in the order of
 * its `attachment_ids`. [staged] names the copy in the staging directory;
 * [fileId] is set once the file is uploaded and never changes after that.
 * [maybeStored] is set while the server may have stored the file but its
 * answer has not arrived. [timeouts] counts the attempts that ran out of time
 * while the file was being sent.
 */
@Entity(
    tableName = "outgoing_files",
    primaryKeys = ["client_message_id", "position"],
    foreignKeys = [
        ForeignKey(
            OutgoingEntity::class,
            ["client_message_id"],
            ["client_message_id"],
            onDelete = ForeignKey.CASCADE,
            onUpdate = ForeignKey.CASCADE,
        ),
    ],
)
data class OutgoingFileEntity(
    @ColumnInfo(name = "client_message_id") val clientMessageId: String,
    val position: Int,
    val name: String,
    /** The type sniffed from the content (FileKind.mime). */
    val type: String,
    val size: Long,
    val staged: String,
    @ColumnInfo(name = "file_id") val fileId: String? = null,
    @ColumnInfo(name = "maybe_stored") val maybeStored: Boolean = false,
    val timeouts: Int = 0,
)

/**
 * The metadata of a file of a room (docs/android/attachments.md), from an
 * upload or `GET /files/{id}`. Files are immutable, so a row never changes.
 * [name] and [contentType] are the server's and untrusted (FileNames,
 * FileKind).
 */
@Entity(
    tableName = "files",
    foreignKeys = [ForeignKey(RoomEntity::class, ["room_id"], ["room_id"], onDelete = ForeignKey.CASCADE)],
    indices = [Index("room_id")],
)
data class FileEntity(
    @PrimaryKey @ColumnInfo(name = "file_id") val fileId: String,
    @ColumnInfo(name = "room_id") val roomId: String,
    @ColumnInfo(name = "owner_id") val ownerId: String,
    val name: String,
    @ColumnInfo(name = "content_type") val contentType: String,
    val size: Long,
    @ColumnInfo(name = "created_at") val createdAt: Long,
)

/**
 * A member of a room whose roster is stored ([RosterEntity]). [joinedAt] is
 * null for a member known only from an event or from the user's own change.
 */
@Entity(
    tableName = "members",
    primaryKeys = ["room_id", "user_id"],
    foreignKeys = [ForeignKey(RoomEntity::class, ["room_id"], ["room_id"], onDelete = ForeignKey.CASCADE)],
)
data class MemberEntity(
    @ColumnInfo(name = "room_id") val roomId: String,
    @ColumnInfo(name = "user_id") val userId: String,
    @ColumnInfo(name = "joined_at") val joinedAt: Long?,
)

/**
 * A room whose members are stored (docs/android/rooms.md). Without a row the
 * roster has not been read. [complete] is false when the room had more
 * members than one read takes.
 */
@Entity(
    tableName = "rosters",
    foreignKeys = [ForeignKey(RoomEntity::class, ["room_id"], ["room_id"], onDelete = ForeignKey.CASCADE)],
)
data class RosterEntity(
    @PrimaryKey @ColumnInfo(name = "room_id") val roomId: String,
    val complete: Boolean,
)

/** A member with their name, for the members screen. */
data class MemberItem(
    @ColumnInfo(name = "user_id") val userId: String,
    @ColumnInfo(name = "username") val username: String?,
    @ColumnInfo(name = "known") val known: Boolean,
)

/** A public profile; [username] is null for an account that no longer exists. */
@Entity(tableName = "profiles")
data class ProfileEntity(
    @PrimaryKey @ColumnInfo(name = "user_id") val userId: String,
    val username: String?,
)

/** A row of the room list. */
data class RoomListItem(
    @Embedded val room: RoomEntity,
    @ColumnInfo(name = "peer_name") val peerName: String?,
    @ColumnInfo(name = "last_text") val lastText: String?,
    @ColumnInfo(name = "last_sender_id") val lastSenderId: String?,
    @ColumnInfo(name = "last_attachments") val lastAttachments: String?,
    @ColumnInfo(name = "last_created_at") val lastCreatedAt: Long?,
    val unread: Int,
    /** Whether unread messages older than the stored block exist, so that [unread] is a lower bound. */
    @ColumnInfo(name = "unread_more") val unreadMore: Boolean,
    /** The last queued message of the room, which is newer than any stored one. */
    @ColumnInfo(name = "pending_text") val pendingText: String?,
    @ColumnInfo(name = "pending_state") val pendingState: String?,
    @ColumnInfo(name = "pending_created_at") val pendingCreatedAt: Long?,
    @ColumnInfo(name = "pending_files") val pendingFiles: Int = 0,
)

/**
 * The newest message of a room that a notification has shown (Notifier), so
 * that no message is shown twice. Messages up to the room's last read one
 * are never shown.
 */
@Entity(
    tableName = "notified",
    foreignKeys = [ForeignKey(RoomEntity::class, ["room_id"], ["room_id"], onDelete = ForeignKey.CASCADE)],
)
data class NotifiedEntity(
    @PrimaryKey @ColumnInfo(name = "room_id") val roomId: String,
    val sequence: Long,
)

/** A room with messages that no notification has shown, up to [newest]. */
data class NoticeRoom(
    @ColumnInfo(name = "room_id") val roomId: String,
    val newest: Long,
)

/** An unread message with what its room's notification needs. */
data class NoticeItem(
    @ColumnInfo(name = "room_id") val roomId: String,
    val sequence: Long,
    @ColumnInfo(name = "sender_id") val senderId: String,
    @ColumnInfo(name = "sender_name") val senderName: String?,
    val text: String,
    val attachments: String,
    @ColumnInfo(name = "created_at") val createdAt: Long,
    @ColumnInfo(name = "room_kind") val roomKind: String,
    @ColumnInfo(name = "room_name") val roomName: String?,
    @ColumnInfo(name = "peer_name") val peerName: String?,
)

/** A message with its sender's name. */
data class MessageItem(
    @Embedded val message: MessageEntity,
    @ColumnInfo(name = "sender_name") val senderName: String?,
    @ColumnInfo(name = "sender_known") val senderKnown: Boolean,
)
