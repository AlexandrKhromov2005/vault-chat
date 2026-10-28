package io.github.alexandrkhromov2005.vaultchat.ui.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.transformLatest

/** A row of the outgoing queue as the conversation shows it; [leftAt] is set once it left the queue. */
data class OutgoingRow(val entity: OutgoingEntity, val leftAt: Long? = null) {
    val clientMessageId: String get() = entity.clientMessageId
}

/**
 * Hands an own message over from the outgoing queue to the stored history on
 * screen (docs/android/send.md, "Shown exactly once").
 *
 * One transaction stores the message and deletes its queue row, but the
 * screen reads the queue and the history through separate observers, which
 * see that transaction one after the other. A row that leaves the queue is
 * therefore kept for a moment, and a row whose stored copy is loaded is
 * hidden, so the message is neither missing nor shown twice in between. The
 * stored copy takes over the row's list key, so the list does not replace it.
 */
object Handoff {
    /** How long a row that left the queue is shown at most while the history catches up. */
    const val LINGER_MS = 2_000L

    /** A queued message shows its clock only once sending takes this long. */
    const val CLOCK_AFTER_MS = 500L

    /**
     * The rows to show after the queue became [queue]: its rows, plus rows
     * that left it, until [LINGER_MS] after they left: a row leaves when its
     * message is stored, a failed one too if the server stored it after all.
     * A failed row in [dismissed] was deleted or queued again under a new key
     * by the user and goes at once.
     */
    fun next(
        previous: List<OutgoingRow>,
        queue: List<OutgoingEntity>,
        now: Long,
        dismissed: Set<String> = emptySet(),
    ): List<OutgoingRow> {
        val queued = queue.mapTo(HashSet()) { it.clientMessageId }
        val leaving = previous.mapNotNull { row ->
            when {
                row.clientMessageId in queued -> null
                row.leftAt != null -> row.takeIf { now - row.leftAt < LINGER_MS }
                row.entity.state == OutgoingEntity.FAILED && row.clientMessageId in dismissed -> null
                else -> row.copy(leftAt = now)
            }
        }
        return (queue.map { OutgoingRow(it) } + leaving).sortedBy { it.entity.position }
    }

    /** Own stored messages among [rows] by client key, the newest one for each key. */
    fun claims(rows: List<ChatRow?>, me: String): Map<String, MessageEntity> {
        val claims = HashMap<String, MessageEntity>()
        for (row in rows) {
            val message = (row as? ChatRow.Message)?.item?.message ?: continue
            if (message.senderId == me) claims.putIfAbsent(message.clientMessageId, message)
        }
        return claims
    }

    /** [rows] without those whose stored copy (same key and text) is loaded. */
    fun visible(rows: List<OutgoingRow>, claims: Map<String, MessageEntity>): List<OutgoingRow> =
        rows.filter { claims[it.clientMessageId]?.text != it.entity.text }

    /**
     * The list key of an outgoing row. It is the one its stored copy will
     * have, unless a stored message already holds that key with another text
     * (the server's `409`).
     */
    fun key(row: OutgoingRow, claims: Map<String, MessageEntity>): String =
        if (row.clientMessageId in claims) "o${row.clientMessageId}" else "c${row.clientMessageId}"

    /**
     * The list key of a stored row: an own message holding its client key
     * takes it, as its outgoing row had it; every other row keeps its own.
     * Keys stay unique even if the server sent two messages with one key.
     */
    fun key(row: ChatRow, claims: Map<String, MessageEntity>): String {
        val message = (row as? ChatRow.Message)?.item?.message ?: return row.key
        return if (claims[message.clientMessageId]?.sequence == message.sequence) "c${message.clientMessageId}" else row.key
    }

    /** How long a message queued at [createdAt] (microseconds) waits before showing its clock. */
    fun clockDelay(createdAt: Long, now: Long): Long =
        (CLOCK_AFTER_MS - (now - createdAt / 1000)).coerceIn(0, CLOCK_AFTER_MS)
}

/**
 * The queue as [Handoff.next] shows it, updated again when a row stops
 * lingering. A key leaves [dismissed] once its row is back in the queue
 * other than failed, so that a later failure is not taken as dismissed.
 */
fun Flow<List<OutgoingEntity>>.handedOver(
    dismissed: MutableSet<String> = mutableSetOf(),
    now: () -> Long,
): Flow<List<OutgoingRow>> = flow {
    var shown = emptyList<OutgoingRow>()
    val updates = transformLatest { queue ->
        queue.forEach { if (it.state != OutgoingEntity.FAILED) dismissed -= it.clientMessageId }
        shown = Handoff.next(shown, queue, now(), dismissed)
        emit(shown)
        while (true) {
            val leftAt = shown.mapNotNull { it.leftAt }.minOrNull() ?: break
            delay((leftAt + Handoff.LINGER_MS - now()).coerceAtLeast(1))
            shown = Handoff.next(shown, queue, now(), dismissed)
            emit(shown)
        }
    }
    emitAll(updates)
}
