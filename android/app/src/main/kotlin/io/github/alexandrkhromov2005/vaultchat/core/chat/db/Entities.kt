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
}

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
)

/** A message with its sender's name. */
data class MessageItem(
    @Embedded val message: MessageEntity,
    @ColumnInfo(name = "sender_name") val senderName: String?,
    @ColumnInfo(name = "sender_known") val senderKnown: Boolean,
)
