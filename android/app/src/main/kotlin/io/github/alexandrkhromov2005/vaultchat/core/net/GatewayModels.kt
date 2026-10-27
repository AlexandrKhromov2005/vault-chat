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

@Serializable
internal data class ErrorEnvelope(val error: ErrorBody? = null)

@Serializable
internal data class ErrorBody(val code: String? = null, val message: String? = null)
