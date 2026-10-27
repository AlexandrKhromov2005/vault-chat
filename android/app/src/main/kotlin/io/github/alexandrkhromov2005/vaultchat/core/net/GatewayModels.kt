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

@Serializable
data class MessagePage(val messages: List<MessageDto>, @SerialName("next_cursor") val nextCursor: String)

@Serializable
data class PublicProfile(@SerialName("user_id") val userId: String, val username: String)
