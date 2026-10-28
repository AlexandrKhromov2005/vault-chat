package io.github.alexandrkhromov2005.vaultchat.core.chat

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull

/**
 * What the background job does (OutboxWorker): sends the queue of the current
 * session within [budgetMs]. Returns true when nothing is left to send, also
 * when nobody is signed in; false asks for another run later.
 */
suspend fun sendQueuedMessages(chats: ChatSessions, budgetMs: Long): Boolean = try {
    // The budget also covers restoring the stored session.
    withTimeoutOrNull(budgetMs) {
        val chat = chats.awaitCurrent() ?: return@withTimeoutOrNull true
        // The database stays open while the job runs, even if the session ends.
        chat.retain().use { chat.outbox.awaitSent() }
        true
    } ?: false
} catch (e: CancellationException) {
    throw e
} catch (e: Exception) {
    // A database that failed or closed under the job: run again later.
    false
}
