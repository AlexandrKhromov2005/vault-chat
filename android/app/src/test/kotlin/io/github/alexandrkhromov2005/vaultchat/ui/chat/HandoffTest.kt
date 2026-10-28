package io.github.alexandrkhromov2005.vaultchat.ui.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class HandoffTest {
    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val bob = "00000000-0000-4000-8000-0000000000bb"
    private val roomId = "00000000-0000-4000-8000-000000000001"

    private fun row(cid: String, position: Long, state: String = OutgoingEntity.QUEUED, text: String = "text $cid") =
        OutgoingEntity(cid, roomId, me, position, text, createdAt = 0, state = state)

    private fun message(sequence: Long, cid: String, sender: String = me, text: String = "text $cid") =
        MessageEntity(roomId, sequence, "m-$sequence", sender, cid, text, attachments = "", createdAt = sequence)

    private fun stored(message: MessageEntity): ChatRow = ChatRow.Message(MessageItem(message, senderName = null, senderKnown = true))

    @Test
    fun rowThatLeavesTheQueueLingersUntilItsTimeIsUp() {
        val a = row("a", 1)
        val b = row("b", 2, OutgoingEntity.SENT)
        var shown = Handoff.next(emptyList(), listOf(a, b), now = 0)
        assertEquals(listOf(OutgoingRow(a), OutgoingRow(b)), shown)

        shown = Handoff.next(shown, emptyList(), now = 100)
        assertEquals(listOf(OutgoingRow(a, leftAt = 100), OutgoingRow(b, leftAt = 100)), shown)

        shown = Handoff.next(shown, emptyList(), now = 100 + Handoff.LINGER_MS - 1)
        assertEquals(2, shown.size)
        assertEquals(emptyList<OutgoingRow>(), Handoff.next(shown, emptyList(), now = 100 + Handoff.LINGER_MS))
    }

    @Test
    fun failedRowTheUserDismissedGoesAtOnce() {
        // Deleted by the user, or queued again under a new key after a conflict.
        val failed = row("a", 1, OutgoingEntity.FAILED)
        val requeued = row("b", 2)
        assertEquals(
            listOf(OutgoingRow(requeued)),
            Handoff.next(listOf(OutgoingRow(failed)), listOf(requeued), now = 5, dismissed = setOf("a")),
        )
    }

    @Test
    fun failedRowThatSyncResolvesLingers() {
        // The server stored it after all.
        val failed = row("a", 1, OutgoingEntity.FAILED)
        assertEquals(listOf(OutgoingRow(failed, leftAt = 5)), Handoff.next(listOf(OutgoingRow(failed)), emptyList(), now = 5))
    }

    @Test
    fun rowRetriedUnderItsKeyLingersWhenStored() {
        // Dismissed while failed, then queued again and stored.
        val queued = row("a", 1)
        assertEquals(
            listOf(OutgoingRow(queued, leftAt = 5)),
            Handoff.next(listOf(OutgoingRow(queued)), emptyList(), now = 5, dismissed = setOf("a")),
        )
    }

    @Test
    fun lingeringRowsKeepTheirPlaceInTheQueue() {
        val a = row("a", 1)
        val b = row("b", 2)
        val c = row("c", 3)
        val shown = Handoff.next(listOf(OutgoingRow(a), OutgoingRow(b)), listOf(a, c), now = 7)
        assertEquals(listOf(OutgoingRow(a), OutgoingRow(b, leftAt = 7), OutgoingRow(c)), shown)
    }

    @Test
    fun rowBackInTheQueueStopsLingering() {
        val a = row("a", 1)
        val shown = Handoff.next(listOf(OutgoingRow(a, leftAt = 3)), listOf(a), now = 5)
        assertEquals(listOf(OutgoingRow(a)), shown)
    }

    @Test
    fun loadedStoredCopyHidesItsRowButAnotherTextDoesNot() {
        val sent = OutgoingRow(row("a", 1))
        val rejected = OutgoingRow(row("b", 2, OutgoingEntity.FAILED, text = "mine"))
        val claims = Handoff.claims(listOf(stored(message(9, "a")), stored(message(8, "b", text = "theirs"))), me)
        assertEquals(listOf(rejected), Handoff.visible(listOf(sent, rejected), claims))
    }

    @Test
    fun storedCopyTakesItsRowsKey() {
        val outgoing = OutgoingRow(row("a", 1))
        val copy = stored(message(9, "a"))
        assertEquals(Handoff.key(outgoing, emptyMap()), Handoff.key(copy, Handoff.claims(listOf(copy), me)))
    }

    @Test
    fun keysStayUniqueWhateverTheServerSends() {
        val newest = stored(message(9, "a"))
        val older = stored(message(8, "a"))
        val bobs = stored(message(7, "b", sender = bob))
        val day = ChatRow.Day(epochDay = 1, beforeSequence = 7)
        val rows = listOf(newest, null, older, bobs, day)
        val claims = Handoff.claims(rows, me)
        // Another text under the newest copy's key, and a row whose key only bob used.
        val outgoing = listOf(OutgoingRow(row("a", 1, OutgoingEntity.FAILED, text = "mine")), OutgoingRow(row("b", 2)))

        val keys = rows.filterNotNull().map { Handoff.key(it, claims) } + outgoing.map { Handoff.key(it, claims) }
        assertEquals(listOf("ca", "m8", "m7", "d7", "oa", "cb"), keys)
    }

    @Test
    fun clockWaitsOnlyForAFreshMessage() {
        val queuedAt = 10_000_000L // microseconds
        assertEquals(Handoff.CLOCK_AFTER_MS, Handoff.clockDelay(queuedAt, now = 10_000))
        assertEquals(Handoff.CLOCK_AFTER_MS - 200, Handoff.clockDelay(queuedAt, now = 10_200))
        assertEquals(0, Handoff.clockDelay(queuedAt, now = 10_000 + Handoff.CLOCK_AFTER_MS))
        assertEquals(0, Handoff.clockDelay(queuedAt, now = 99_000))
        // A clock set back does not hide it for longer.
        assertEquals(Handoff.CLOCK_AFTER_MS, Handoff.clockDelay(queuedAt, now = 1_000))
    }

    @Test
    fun flowDropsALingeringRowWhenItsTimeIsUp() = runTest {
        val a = row("a", 1)
        val queue = MutableStateFlow(listOf(a))
        val seen = mutableListOf<List<OutgoingRow>>()
        val job = launch { queue.handedOver { testScheduler.currentTime }.toList(seen) }
        runCurrent()
        advanceTimeBy(100)
        queue.value = emptyList()
        runCurrent()
        advanceTimeBy(Handoff.LINGER_MS - 1)
        runCurrent()
        assertEquals(listOf(listOf(OutgoingRow(a)), listOf(OutgoingRow(a, leftAt = 100))), seen)

        advanceTimeBy(1)
        runCurrent()
        assertEquals(emptyList<OutgoingRow>(), seen.last())
        job.cancel()
    }
}
