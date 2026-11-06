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

/** Where notices show: the system's notifications, in the app. */
interface NoticeBoard {
    /**
     * Shows [notices], each replacing its room's notification. [account] names
     * the session they belong to, so that a tap opens them only for that one.
     */
    fun post(account: String, notices: List<RoomNotice>)

    /** The rooms with a notification showing. */
    fun shownRooms(): Set<String>

    fun cancel(roomId: String)

    object None : NoticeBoard {
        override fun post(account: String, notices: List<RoomNotice>) = Unit

        override fun shownRooms(): Set<String> = emptySet()

        override fun cancel(roomId: String) = Unit
    }
}

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
    private val board: NoticeBoard,
    private val account: String,
    scope: CoroutineScope,
) {
    private val lock = Mutex()

    // Held around what touches the board, and never across a suspension.
    private val ending = Any()
    private var stopped = false

    init {
        scope.launch {
            combine(dao.noticeRoomsFlow(me), foreground) { _, _ -> }.collect {
                try {
                    check(waitForNames = true)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // A database failure; the next change tries again.
                }
            }
        }
    }

    /**
     * Shows what is new now, and removes the notifications of rooms that are
     * gone; the background sync calls it before it ends. Does nothing once the
     * session has ended.
     *
     * Messages arrive before their senders' names, and a mark leaves no second
     * chance to show the name. With [waitForNames], a room with a sender whose
     * name is not stored yet stays until it is, which runs this again; the
     * background sync's call shows what it has, so that a name that cannot be
     * read holds nothing back for longer than until the next sync.
     */
    suspend fun check(waitForNames: Boolean = false) = lock.withLock {
        if (synchronized(ending) { stopped }) return@withLock
        // Marked before they are posted: a mark that fails must not alert again
        // at every later check. What can go wrong after it loses one alert, and
        // the message stays unread in the app.
        val notices = dao.takeNotices(me, MAX_SHOWN, waitForNames && !foreground.value)
        val rooms = dao.roomIds().toSet()
        synchronized(ending) {
            if (stopped) return@withLock
            if (notices.isNotEmpty() && !foreground.value) board.post(account, notices)
            (board.shownRooms() - rooms).forEach(board::cancel)
        }
    }

    /**
     * Ends all showing. It waits for a post under way, so that what removes the
     * session's notifications after this removes them for good.
     */
    fun stop() {
        synchronized(ending) { stopped = true }
    }

    companion object {
        /** Messages of a room in one notification. */
        const val MAX_SHOWN = 5
    }
}
