package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.NoticeItem
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** What the notification of one room shows: its newest unread messages, oldest first. */
data class RoomNotice(val roomId: String, val messages: List<NoticeItem>)

/**
 * Shows new messages of others as notifications (docs/android/push.md)
 * while the app is not on screen, however they arrived: a push that woke the
 * background sync, the periodic sync or the WebSocket just before the app
 * left the screen. On screen, new messages are only marked as shown, so that
 * they do not pop up later. Each message alerts at most once.
 */
class Notifier(
    private val dao: ChatDao,
    private val me: String,
    private val foreground: StateFlow<Boolean>,
    private val post: (List<RoomNotice>) -> Unit,
    scope: CoroutineScope,
) {
    private val lock = Mutex()

    init {
        scope.launch {
            combine(dao.noticeRoomsFlow(me), foreground) { _, _ -> }.collect {
                try {
                    check()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // A database failure; the next change tries again.
                }
            }
        }
    }

    /** Shows what is new now; the background sync calls it before it ends. */
    suspend fun check() = lock.withLock {
        // Marked before they are posted: a mark that fails must not alert again
        // at every later check. What can go wrong after it loses one alert, and
        // the message stays unread in the app.
        val notices = dao.takeNotices(me, MAX_SHOWN)
        if (notices.isNotEmpty() && !foreground.value) post(notices)
    }

    companion object {
        /** Messages of a room in one notification. */
        const val MAX_SHOWN = 5
    }
}
