package io.github.alexandrkhromov2005.vaultchat.core.chat

import kotlinx.coroutines.withTimeoutOrNull

/**
 * What the background job does (OutboxWorker): sends the queue of the current
 * session within [budgetMs]. Returns true when nothing is left to send, also
 * when nobody is signed in; false asks for another run later.
 */
suspend fun sendQueuedMessages(chats: ChatSessions, budgetMs: Long): Boolean {
    val chat = chats.awaitCurrent() ?: return true
    // The database stays open while the job runs, even if the session ends.
    return chat.retain().use {
        withTimeoutOrNull(budgetMs) { chat.outbox.awaitSent() } != null
    }
}
