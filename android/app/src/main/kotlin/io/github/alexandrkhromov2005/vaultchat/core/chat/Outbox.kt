package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.SendMessageRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withTimeoutOrNull
import java.util.UUID

/**
 * The outgoing queue of a session (docs/android/send.md). Messages are stored
 * first and sent afterwards: in order within a room, one at a time, at most
 * [CONCURRENT_SENDS] rooms at once. A send whose outcome is unknown is
 * repeated with the same key, which the server answers with the original
 * message, so a message is never stored twice.
 */
class Outbox(
    private val dao: ChatDao,
    private val me: String,
    private val requests: ChatRequests,
    private val sync: ChatSync,
    private val scope: CoroutineScope,
    /** The current network, or null without one; nothing is sent without one. */
    private val network: StateFlow<Any?>,
    /** Wall-clock time of queuing in microseconds, shown next to the message. */
    private val nowMicros: () -> Long = { System.currentTimeMillis() * 1000 },
    private val newId: () -> String = { UUID.randomUUID().toString() },
    private val backoff: () -> Backoff = { Backoff(baseMs = 1_000, maxMs = 60_000) },
    /** Called after a message is queued, so that it is sent in the background too. */
    private val onQueued: () -> Unit = {},
) {
    private val gate = sync.gate
    private val permits = Semaphore(CONCURRENT_SENDS)

    // Rooms whose queue is being sent; guarded by itself.
    private val sending = mutableSetOf<String>()

    // A change of either starts the rooms that have something to send; a
    // kick also cuts their backoff short.
    private val wakes = MutableStateFlow(0L)
    private val kicks = MutableStateFlow(0L)

    init {
        // The first value also picks up a queue left by an earlier process.
        scope.launch { wakes.collect { startRooms() } }
    }

    val queuedCount: Flow<Int> get() = dao.queuedCount()

    /** Messages the server has not stored: queued and failed ones. */
    val unsentCount: Flow<Int> get() = dao.unsentCount()

    fun outgoing(roomId: String): Flow<List<OutgoingEntity>> = dao.outgoing(roomId)

    /**
     * Queues [text] for [roomId] unchanged. Returns why it cannot be sent, or
     * null once it is queued. A room that is no longer stored is gone for the
     * user, and the text is dropped with it.
     */
    suspend fun send(roomId: String, text: String): MessageText.Problem? {
        MessageText.check(text)?.let { return it }
        val row = OutgoingEntity(
            clientMessageId = newId(),
            roomId = roomId,
            senderId = me,
            position = 0,
            text = text,
            createdAt = nowMicros(),
            state = OutgoingEntity.QUEUED,
        )
        if (dao.enqueue(row)) queued()
        return null
    }

    /**
     * Queues a failed message again. It keeps its key, except after a
     * conflict: that key is taken by another text, so this text was never
     * stored and a new key cannot duplicate it.
     */
    suspend fun retry(clientMessageId: String) {
        val row = dao.outgoingRow(clientMessageId)?.takeIf { it.state == OutgoingEntity.FAILED } ?: return
        val key = if (row.error == OutgoingEntity.ERROR_CONFLICT) newId() else row.clientMessageId
        if (dao.requeue(row.clientMessageId, key)) queued()
    }

    /** Deletes a failed message. A queued one may already be stored, so it stays. */
    suspend fun delete(clientMessageId: String) {
        dao.deleteFailed(clientMessageId)
    }

    /**
     * Sends at once what waits for a backoff: a network appeared, the
     * WebSocket is live again or the app came to the foreground.
     */
    fun kick() {
        kicks.update { it + 1 }
        wake()
    }

    /** Sends what is queued now and returns once nothing is queued any more. */
    suspend fun awaitSent() {
        kick()
        dao.queuedCount().first { it == 0 }
    }

    private fun queued() {
        wake()
        onQueued()
    }

    private fun wake() {
        wakes.update { it + 1 }
    }

    private suspend fun startRooms() {
        for (roomId in dao.queuedRooms()) {
            if (!synchronized(sending) { sending.add(roomId) }) continue
            scope.launch { sendRoom(roomId) }
        }
    }

    /** Sends the queue of one room until it is empty. */
    private suspend fun sendRoom(roomId: String) {
        val delays = backoff()
        while (true) {
            val row = dao.nextQueued(roomId)
            if (row == null) {
                synchronized(sending) { sending.remove(roomId) }
                // A message queued after the check above and before the room
                // left the set started nothing.
                if (dao.nextQueued(roomId) != null) wake()
                return
            }
            network.first { it != null }
            // A kick during the attempt, such as a new network, also cuts the backoff after it short.
            val seen = kicks.value
            val outcome = try {
                permits.withPermit {
                    while (gate.remaining() > 0) delay(gate.remaining())
                    attempt(row)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // A database failure; the row stays queued.
                Outcome.RETRY
            }
            when (outcome) {
                Outcome.DONE -> delays.reset()
                Outcome.PAUSED -> Unit
                Outcome.RETRY -> {
                    withTimeoutOrNull(delays.next()) { kicks.first { it != seen } }
                }
            }
        }
    }

    private enum class Outcome { DONE, RETRY, PAUSED }

    private suspend fun attempt(row: OutgoingEntity): Outcome {
        val message = try {
            requests.send { sendMessage(it, row.roomId, SendMessageRequest(row.clientMessageId, row.text)) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            return when {
                e.status == 429 -> {
                    gate.pause(e.retryAfterSeconds)
                    Outcome.PAUSED
                }
                e.code == ApiException.INVALID_RESPONSE -> fail(row, OutgoingEntity.ERROR_INVALID)
                // The outcome is unknown, or the session renews its token.
                e.status == 0 || e.status == 401 || e.status == 408 || e.status >= 500 -> Outcome.RETRY
                e.status == 403 || e.status == 404 -> fail(row, OutgoingEntity.ERROR_NO_ACCESS)
                e.status == 409 -> fail(row, OutgoingEntity.ERROR_CONFLICT)
                else -> fail(row, OutgoingEntity.ERROR_INVALID)
            }
        }
        val stored = try {
            message.toEntity(row.roomId).also { it.check(row) }
        } catch (e: ApiException) {
            // If the server stored it after all, sync brings it and resolves the row.
            return fail(row, OutgoingEntity.ERROR_INVALID)
        }
        dao.markSent(row.roomId, row.clientMessageId, stored.sequence)
        sync.receive(stored)
        return Outcome.DONE
    }

    /** The answer must be the queued message itself. */
    private fun MessageEntity.check(row: OutgoingEntity) {
        if (senderId != me || clientMessageId != row.clientMessageId || text != row.text) throw invalid("not the queued message")
    }

    private suspend fun fail(row: OutgoingEntity, error: String): Outcome {
        dao.markFailed(row.clientMessageId, error)
        return Outcome.DONE
    }

    companion object {
        const val CONCURRENT_SENDS = 2
    }
}
