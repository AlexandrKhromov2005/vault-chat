package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.ComponentName
import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.paging.PagingData
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.RuleChain
import org.junit.runner.RunWith
import org.junit.runners.model.Statement
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf

/**
 * The hand-over of an own message from the outgoing queue to the stored
 * history. The two are read by separate observers, so the screen sees them
 * change one after the other; the message must stay on screen exactly once.
 */
@RunWith(RobolectricTestRunner::class)
class ConversationListTest {
    private val compose = createComposeRule()

    // The app declares no activity to host the content in; the test
    // registers one rather than ship an exported activity in debug builds.
    @get:Rule
    val rules: RuleChain = RuleChain.outerRule { base, _ ->
        object : Statement() {
            override fun evaluate() {
                val context = ApplicationProvider.getApplicationContext<Context>()
                shadowOf(context.packageManager).addActivityIfNotPresent(ComponentName(context, ComponentActivity::class.java))
                base.evaluate()
            }
        }
    }.around(compose)

    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val bob = "00000000-0000-4000-8000-0000000000bb"
    private val roomId = "00000000-0000-4000-8000-000000000001"
    private val now = 1_800_000_000_000_000L

    private val pages = MutableStateFlow(PagingData.from(listOf<ChatRow>(stored(4, bob, "cid-4", "earlier"))))
    private val queue = MutableStateFlow(listOf(outgoing("cid-5", "handoff", OutgoingEntity.QUEUED)))

    private fun stored(sequence: Long, sender: String, cid: String, text: String): ChatRow =
        ChatRow.Message(
            MessageItem(
                MessageEntity(roomId, sequence, "m-$sequence", sender, cid, text, attachments = "", createdAt = now + sequence),
                senderName = "bob",
                senderKnown = true,
            ),
        )

    private fun outgoing(cid: String, text: String, state: String, sequence: Long? = null) =
        OutgoingEntity(cid, roomId, me, position = 1, text = text, createdAt = now + 5, state = state, sequence = sequence)

    private fun show() {
        compose.setContent {
            val rows = pages.collectAsLazyPagingItems()
            // As the screen's model reads the queue.
            val outgoing by remember { queue.handedOver { 0L } }.collectAsState(emptyList())
            Messages(rows, outgoing, me, channel = false, onNewestSeen = {}, onRetry = {}, onDelete = {})
        }
        compose.waitForIdle()
    }

    private fun shown(text: String): Int =
        compose.onAllNodes(hasContentDescription(text, substring = true)).fetchSemanticsNodes().size

    @Test
    fun messageThatLeavesTheQueueBeforeTheHistoryShowsItStaysOnScreen() {
        show()
        assertEquals(1, shown("handoff"))

        // The queue's observer sees the transaction first.
        queue.value = emptyList()
        compose.waitForIdle()
        assertEquals("shown while the history reloads", 1, shown("handoff"))

        pages.value = PagingData.from(listOf(stored(5, me, "cid-5", "handoff"), stored(4, bob, "cid-4", "earlier")))
        compose.waitForIdle()
        assertEquals(1, shown("handoff"))
    }

    @Test
    fun messageThatReachesTheHistoryBeforeTheQueueClearsIsShownOnce() {
        queue.value = listOf(outgoing("cid-5", "handoff", OutgoingEntity.SENT, sequence = 5))
        show()

        // The history's observer sees the transaction first.
        pages.value = PagingData.from(listOf(stored(5, me, "cid-5", "handoff"), stored(4, bob, "cid-4", "earlier")))
        compose.waitForIdle()
        assertEquals("shown once while the queue catches up", 1, shown("handoff"))

        queue.value = emptyList()
        compose.waitForIdle()
        assertEquals(1, shown("handoff"))
    }

    @Test
    fun rejectedTextNextToTheStoredMessageWithItsKeyIsShownBesideIt() {
        // The server's 409: the key holds another text, which is shown as stored.
        queue.value = listOf(outgoing("cid-5", "rejected", OutgoingEntity.FAILED))
        pages.value = PagingData.from(listOf(stored(5, me, "cid-5", "accepted"), stored(4, bob, "cid-4", "earlier")))
        show()

        assertEquals(1, shown("rejected"))
        assertEquals(1, shown("accepted"))
    }

    @Test
    fun ownMessagesThatShareAKeyAreAllShown() {
        // A server that breaks the key's uniqueness must not break the list's.
        pages.value = PagingData.from(listOf(stored(6, me, "cid-5", "twice"), stored(5, me, "cid-5", "twice")))
        show()

        assertEquals(2, shown("twice"))
        assertEquals(1, shown("handoff"))
    }
}
