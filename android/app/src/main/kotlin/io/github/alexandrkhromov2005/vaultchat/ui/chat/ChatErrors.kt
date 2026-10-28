package io.github.alexandrkhromov2005.vaultchat.ui.chat

import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.ui.auth.Message

/** What a room or member request does, for the parts of its errors that depend on it. */
internal enum class ChatAction { SEARCH, OPEN_DIALOG, CREATE_CHANNEL, ROSTER, ADD_MEMBER, REMOVE_MEMBER, LEAVE }

/** The message for a failed request of [action] (docs/android/rooms.md). */
internal fun describe(error: ApiException, action: ChatAction): Message = when {
    error.code == ApiException.NETWORK -> Message(R.string.chat_error_network)
    error.code == ApiException.INVALID_RESPONSE -> Message(R.string.chat_error_invalid)
    error.code == ApiException.LOCAL -> Message(R.string.chat_error_local)
    error.status == 429 -> error.retryAfterSeconds
        ?.let { Message(R.string.error_rate_limited_for, listOf(it)) }
        ?: Message(R.string.error_rate_limited)
    error.status == 404 -> Message(
        when (action) {
            ChatAction.OPEN_DIALOG, ChatAction.ADD_MEMBER -> R.string.chat_error_user_not_found
            else -> R.string.chat_error_no_access
        },
    )
    error.status == 403 -> Message(R.string.error_forbidden)
    error.status == 409 -> Message(R.string.chat_error_owner)
    error.status == 400 -> Message(if (action == ChatAction.CREATE_CHANNEL) R.string.chat_error_name else R.string.chat_error_invalid_input)
    error.status == 503 || error.status == 504 || error.status == 408 -> Message(R.string.error_unavailable)
    else -> Message(R.string.error_unexpected, listOf(error.status))
}
