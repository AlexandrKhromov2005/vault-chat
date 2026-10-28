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
    /**
     * Sends, repeats included, keep to this budget, so that an outage with
     * many queued rooms stays inside the Gateway's limit next to sync's.
     */
    private val budget: RequestBudget = RequestBudget(SENDS_PER_MINUTE, SEND_BURST) { System.nanoTime() / 1_000_000 },
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
        scope.launch {
            wakes.collect {
                try {
                    startRooms()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // A database failure must not end sending; try again soon.
                    scope.launch {
                        delay(RESTART_AFTER_FAILURE_MS)
                        wake()
                    }
                }
            }
        }
    }

    val queuedCount: Flow<Int> get() = dao.queuedCount()

    /** Messages the server has not stored: queued and failed ones. */
    val unsentCount: Flow<Int> get() = dao.unsentCount()

    fun outgoing(roomId: String): Flow<List<OutgoingEntity>> = dao.outgoing(roomId)

    /**
     * Queues [text] for [roomId] unchanged. Returns whether it was queued: not
     * if the server would reject the text (MessageText) or the room is no
     * longer stored, so that the composer keeps it.
     */
    suspend fun send(roomId: String, text: String): Boolean {
        if (MessageText.check(text) != null) return false
        val row = OutgoingEntity(
            clientMessageId = newId(),
            roomId = roomId,
            senderId = me,
            position = 0,
            text = text,
            createdAt = nowMicros(),
            state = OutgoingEntity.QUEUED,
        )
        if (!dao.enqueue(row)) return false
        queued()
        return true
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

    /**
     * Sends the queue of one room until it is empty. The room stays in
     * [sending] until then, also through failures, so that no second sender
     * for it can start and break its order.
     */
    private suspend fun sendRoom(roomId: String) {
        val delays = backoff()
        // Whether the next attempt repeats one that did not end its row.
        var repeat = false
        while (true) {
            // A kick during the attempt, such as a new network, also cuts the backoff after it short.
            val seen = kicks.value
            val outcome = try {
                sendNext(roomId, repeat)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // A database failure; the row stays queued.
                Outcome.RETRY
            }
            repeat = outcome == Outcome.RETRY || outcome == Outcome.PAUSED
            when (outcome) {
                Outcome.EMPTY -> return
                Outcome.DONE -> delays.reset()
                Outcome.PAUSED -> Unit
                Outcome.RETRY -> {
                    withTimeoutOrNull(delays.next()) { kicks.first { it != seen } }
                }
            }
        }
    }

    /**
     * Sends the room's next row. A first attempt goes at once while the
     * budget is in debt of less than its burst, so that a few quick messages
     * are not held up, but a queue written offline still drains at the
     * budget's rate; a [repeat] waits for a token.
     */
    private suspend fun sendNext(roomId: String, repeat: Boolean): Outcome {
        val row = dao.nextQueued(roomId)
        if (row == null) {
            synchronized(sending) { sending.remove(roomId) }
            // A message queued after the check above and before the room left
            // the set started nothing. If the check fails, a wake does no harm.
            val more = try {
                dao.nextQueued(roomId) != null
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                true
            }
            if (more) wake()
            return Outcome.EMPTY
        }
        network.first { it != null }
        awaitPause()
        if (repeat) budget.take() else budget.takeWithinDebt()
        return permits.withPermit {
            // A 429 may have started a pause while this room waited for a permit.
            awaitPause()
            attempt(row)
        }
    }

    private suspend fun awaitPause() {
        while (gate.remaining() > 0) delay(gate.remaining())
    }

    private enum class Outcome { EMPTY, DONE, RETRY, PAUSED }

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

        // With sync's 60/min and burst 15, inside the Gateway's 120/min and 30.
        const val SENDS_PER_MINUTE = 30
        const val SEND_BURST = 10

        private const val RESTART_AFTER_FAILURE_MS = 1_000L
    }
}
