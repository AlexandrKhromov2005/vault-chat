package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.MessageDto
import io.github.alexandrkhromov2005.vaultchat.core.net.MessagePage
import io.github.alexandrkhromov2005.vaultchat.core.net.RoomDto
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException

// Checks of what the Gateway returns, before anything is stored. Messages of
// a room are numbered without gaps (docs/chat/messages.md), so a page whose
// sequences are not consecutive, or that belongs to another room, is not
// what the API describes and is rejected as a whole.

private val UUID = Regex("[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")

/** The Gateway's limits on a message (docs/api/gateway.yaml). */
const val MAX_TEXT_CODE_POINTS = 4096
const val MAX_ATTACHMENTS = 5

fun isUuid(value: String): Boolean = UUID.matches(value)

internal fun invalid(reason: String): ApiException = ApiException(200, ApiException.INVALID_RESPONSE, reason)

private fun uuid(value: String, field: String): String = value.takeIf(::isUuid) ?: throw invalid("$field is not a UUID")

/**
 * Microseconds since the epoch of an RFC 3339 time. The server's times have
 * microseconds, and two messages may fall in the same millisecond, so a
 * coarser unit would make them look equally new.
 */
fun parseMicros(value: String): Long? = try {
    val instant = OffsetDateTime.parse(value).toInstant()
    Math.addExact(Math.multiplyExact(instant.epochSecond, 1_000_000L), instant.nano / 1_000L)
} catch (e: DateTimeParseException) {
    null
} catch (e: ArithmeticException) {
    null
}

private fun time(value: String, field: String): Long = parseMicros(value) ?: throw invalid("$field is not a time")

internal fun MessageDto.toEntity(expectedRoomId: String): MessageEntity {
    if (roomId != expectedRoomId) throw invalid("message of another room")
    if (sequence < 1) throw invalid("sequence below 1")
    if (text.codePointCount(0, text.length) > MAX_TEXT_CODE_POINTS) throw invalid("text too long")
    val attachments = attachmentIds.orEmpty()
    if (attachments.size > MAX_ATTACHMENTS) throw invalid("too many attachments")
    return MessageEntity(
        roomId = roomId,
        sequence = sequence,
        messageId = uuid(messageId, "message_id"),
        senderId = uuid(senderId, "sender_id"),
        clientMessageId = uuid(clientMessageId, "client_message_id"),
        text = text,
        attachments = attachments.joinToString(",") { uuid(it, "attachment_id") },
        createdAt = time(createdAt, "created_at"),
    )
}

/**
 * A history page, newest first, read with [cursor] (null for the latest
 * page): consecutive sequences below the cursor.
 */
internal fun historyPage(roomId: String, cursor: Long?, limit: Int, page: MessagePage): List<MessageEntity> {
    val messages = page.messages.map { it.toEntity(roomId) }
    if (messages.size > limit) throw invalid("page over its limit")
    messages.zipWithNext().forEach { (newer, older) ->
        if (older.sequence != newer.sequence - 1) throw invalid("history is not consecutive")
    }
    if (cursor != null && messages.isNotEmpty() && messages.first().sequence >= cursor) {
        throw invalid("history page not before its cursor")
    }
    return messages
}

/**
 * A catch-up page, oldest first: consecutive sequences starting right after
 * [after]. Returns the messages and whether more pages follow.
 */
internal fun catchUpPage(roomId: String, after: Long, limit: Int, page: MessagePage): Pair<List<MessageEntity>, Boolean> {
    val messages = page.messages.map { it.toEntity(roomId) }
    if (messages.size > limit) throw invalid("page over its limit")
    messages.forEachIndexed { index, message ->
        if (message.sequence != after + 1 + index) throw invalid("catch-up is not consecutive")
    }
    val more = page.nextCursor.isNotEmpty()
    // A page that promises more but brings nothing would never end.
    if (more && messages.isEmpty()) throw invalid("empty catch-up page with a cursor")
    return messages to more
}

/** A room as stored for [me], placed at [activity] in the room list. */
internal fun RoomDto.toEntity(me: String, activity: Long): RoomEntity {
    val participants = participantIds.orEmpty().map { uuid(it, "participant_id") }
    return RoomEntity(
        roomId = uuid(roomId, "room_id"),
        kind = kind,
        name = name,
        ownerId = ownerId?.let { uuid(it, "owner_id") },
        isPrivate = private,
        createdAt = time(createdAt, "created_at"),
        peerId = if (kind == RoomEntity.DIRECT) participants.firstOrNull { it != me } else null,
        lastMessageAt = lastMessageAt?.let { time(it, "last_message_at") },
        activity = activity,
    )
}
