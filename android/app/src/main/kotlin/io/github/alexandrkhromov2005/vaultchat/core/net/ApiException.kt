package io.github.alexandrkhromov2005.vaultchat.core.net

/**
 * A failed Gateway request. [status] is the HTTP status, or 0 when the Gateway
 * could not be reached; [code] is the Gateway's machine-readable error code
 * from `{"error":{"code","message"}}`, or one of the client codes below.
 */
open class ApiException(
    val status: Int,
    val code: String,
    message: String,
    val retryAfterSeconds: Long? = null,
    cause: Throwable? = null,
) : Exception(message, cause) {

    val isUnauthenticated: Boolean get() = status == 401

    companion object {
        /** The Gateway could not be reached or the connection broke. */
        const val NETWORK = "network"

        /** A response that does not match the API: not JSON, or missing fields. */
        const val INVALID_RESPONSE = "invalid_response"

        /** An error response without the Gateway's error envelope. */
        const val HTTP_ERROR = "http_error"

        /** The session could not be stored on this device. */
        const val STORAGE = "storage"
    }
}
