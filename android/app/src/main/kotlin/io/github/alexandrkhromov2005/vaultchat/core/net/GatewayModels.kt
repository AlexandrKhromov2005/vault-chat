package io.github.alexandrkhromov2005.vaultchat.core.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// Request and response bodies of docs/api/gateway.yaml. Unknown fields are
// ignored, so the server can add fields without breaking older clients.

@Serializable
data class LoginRequest(val email: String, val password: String)

@Serializable
data class RegisterRequest(
    val email: String,
    val username: String,
    val password: String,
    @SerialName("invitation_token") val invitationToken: String,
)

@Serializable
data class RefreshTokenRequest(@SerialName("refresh_token") val refreshToken: String)

@Serializable
data class TokenPair(
    @SerialName("user_id") val userId: String,
    @SerialName("access_token") val accessToken: String,
    @SerialName("refresh_token") val refreshToken: String,
    @SerialName("access_expires_at") val accessExpiresAt: String,
)

@Serializable
data class User(
    @SerialName("user_id") val userId: String,
    val username: String,
    val email: String? = null,
)

/** A created account; the server may require the email to be confirmed before login. */
@Serializable
data class Registration(
    @SerialName("user_id") val userId: String,
    val username: String,
    val email: String? = null,
    @SerialName("verification_required") val verificationRequired: Boolean = false,
)

@Serializable
data class EmailVerificationRequest(val email: String, val password: String)

/** The same answer whether or not an email is sent. */
@Serializable
data class EmailAcknowledgement(val message: String? = null)

@Serializable
internal data class ErrorEnvelope(val error: ErrorBody? = null)

@Serializable
internal data class ErrorBody(val code: String? = null, val message: String? = null)

/** A room as listed by `GET /api/v1/rooms` or read by `GET /api/v1/rooms/{room_id}`. */
@Serializable
data class RoomDto(
    @SerialName("room_id") val roomId: String,
    val kind: String,
    val name: String? = null,
    @SerialName("owner_id") val ownerId: String? = null,
    val private: Boolean = false,
    @SerialName("created_at") val createdAt: String,
    @SerialName("participant_ids") val participantIds: List<String>? = null,
    @SerialName("last_message_at") val lastMessageAt: String? = null,
)

@Serializable
data class RoomPage(val rooms: List<RoomDto>, @SerialName("next_cursor") val nextCursor: String)

@Serializable
data class MessageDto(
    @SerialName("message_id") val messageId: String,
    @SerialName("room_id") val roomId: String,
    @SerialName("sender_id") val senderId: String,
    @SerialName("client_message_id") val clientMessageId: String,
    val text: String,
    @SerialName("attachment_ids") val attachmentIds: List<String>? = null,
    val sequence: Long,
    @SerialName("created_at") val createdAt: String,
)

/** [attachmentIds] is left out of a text message, as the server expects. */
@Serializable
data class SendMessageRequest(
    @SerialName("client_message_id") val clientMessageId: String,
    val text: String,
    @SerialName("attachment_ids") val attachmentIds: List<String>? = null,
)

@Serializable
data class MessagePage(val messages: List<MessageDto>, @SerialName("next_cursor") val nextCursor: String)

@Serializable
data class PublicProfile(@SerialName("user_id") val userId: String, val username: String)

@Serializable
data class UserPage(val users: List<PublicProfile>)

@Serializable
data class OpenDirectRequest(@SerialName("peer_user_id") val peerUserId: String)

@Serializable
data class CreateChannelRequest(val name: String, val private: Boolean)

@Serializable
data class MemberDto(@SerialName("user_id") val userId: String, @SerialName("joined_at") val joinedAt: String)

@Serializable
data class MemberPage(val members: List<MemberDto>, @SerialName("next_cursor") val nextCursor: String)

/** A device's push token (docs/push.md). */
@Serializable
data class PushTokenRequest(val provider: String, val token: String)

/** A file's metadata (`File` in docs/api/gateway.yaml). */
@Serializable
data class FileDto(
    @SerialName("file_id") val fileId: String,
    @SerialName("room_id") val roomId: String,
    @SerialName("owner_id") val ownerId: String,
    val filename: String,
    @SerialName("content_type") val contentType: String,
    val size: Long,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class CallCommand(@SerialName("command_id") val commandId: String)

@Serializable
data class CreateCallRequest(
    @SerialName("room_id") val roomId: String,
    @SerialName("command_id") val commandId: String,
)

@Serializable
data class CallDto(
    @SerialName("call_id") val callId: String,
    @SerialName("room_id") val roomId: String,
    val state: String,
    @SerialName("state_version") val stateVersion: Long = 0,
    @SerialName("caller_id") val callerId: String,
    @SerialName("callee_id") val calleeId: String,
)

@Serializable
data class CallPage(val calls: List<CallDto> = emptyList())

@Serializable
data class CallSignalRequest(
    val kind: String,
    val payload: String,
    @SerialName("sender_seq") val senderSeq: Long,
)

@Serializable
data class CallSignalDto(
    @SerialName("signal_seq") val signalSeq: Long,
    @SerialName("sender_device") val senderDevice: String = "",
    @SerialName("sender_seq") val senderSeq: Long,
    val kind: String,
    val payload: String,
)

@Serializable
data class CallSignalPage(val signals: List<CallSignalDto> = emptyList())

@Serializable
data class CallSignalResult(
    val state: String,
    @SerialName("signal_seq") val signalSeq: Long,
)

@Serializable
data class TurnResponse(
    @SerialName("ice_servers") val iceServers: List<IceServerDto>,
    val ttl: Long = 600,
)

@Serializable
data class IceServerDto(
    val urls: List<String>,
    val username: String? = null,
    val credential: String? = null,
)

@Serializable
data class EmptyCallBody(val marker: Boolean? = null)
