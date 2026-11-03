package io.github.alexandrkhromov2005.vaultchat

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.Person
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileNames
import io.github.alexandrkhromov2005.vaultchat.core.chat.NoticeBoard
import io.github.alexandrkhromov2005.vaultchat.core.chat.RoomNotice
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.NoticeItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.isUuid
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.getAndUpdate

/**
 * The app's notifications (docs/android/push.md): one per room with its new
 * messages. What the lock screen shows follows the user's system setting: the
 * public version says only "new message", and it is used only when the user
 * chose to hide sensitive content there.
 */
object Notifications {
    const val MESSAGES = "messages"
    const val SYNC = "sync"
    const val EXTRA_ROOM = "io.github.alexandrkhromov2005.vaultchat.ROOM"
    const val EXTRA_ACCOUNT = "io.github.alexandrkhromov2005.vaultchat.ACCOUNT"

    /** The id of the background sync's own notification, below Android 12 only. */
    const val SYNC_ID = 1

    fun createChannels(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(
            NotificationChannel(MESSAGES, context.getString(R.string.channel_messages), NotificationManager.IMPORTANCE_HIGH),
        )
        manager.createNotificationChannel(
            NotificationChannel(SYNC, context.getString(R.string.channel_sync), NotificationManager.IMPORTANCE_MIN),
        )
    }

    fun post(context: Context, account: String, notices: List<RoomNotice>) {
        val manager = NotificationManagerCompat.from(context)
        if (!manager.areNotificationsEnabled()) return
        for (notice in notices) {
            try {
                manager.notify(notice.roomId, MESSAGE_ID, build(context, account, notice))
            } catch (e: SecurityException) {
                // The permission was taken back meanwhile.
                return
            }
        }
    }

    /** Removes the notification of a room the user has opened or left. */
    fun cancel(context: Context, roomId: String) {
        NotificationManagerCompat.from(context).cancel(roomId, MESSAGE_ID)
    }

    /**
     * Removes every notification of the app, for when its account is gone. The
     * background sync's own notification belongs to a foreground service, which
     * this leaves alone.
     */
    fun cancelAll(context: Context) {
        NotificationManagerCompat.from(context).cancelAll()
    }

    /** The rooms with a notification showing. */
    fun shownRooms(context: Context): Set<String> =
        NotificationManagerCompat.from(context).activeNotifications
            .filter { it.id == MESSAGE_ID }
            .mapNotNullTo(HashSet()) { it.tag }

    private fun build(context: Context, account: String, notice: RoomNotice): android.app.Notification {
        val first = notice.messages.first()
        val channel = first.roomKind == RoomEntity.CHANNEL
        val title = if (channel) {
            first.roomName?.takeIf { it.isNotBlank() } ?: context.getString(R.string.room_kind_channel)
        } else {
            first.peerName ?: context.getString(R.string.room_kind_direct)
        }
        val me = Person.Builder().setName(context.getString(R.string.sender_you)).build()
        val style = NotificationCompat.MessagingStyle(me)
            .setConversationTitle(if (channel) title else null)
            .setGroupConversation(channel)
        for (message in notice.messages) {
            val sender = Person.Builder()
                .setName(message.senderName?.let(FileNames::display) ?: context.getString(R.string.sender_unknown))
                .setKey(message.senderId)
                .build()
            style.addMessage(text(context, message), message.createdAt / 1000, sender)
        }
        val open = Intent(context, MainActivity::class.java)
            .setAction(Intent.ACTION_VIEW)
            .putExtra(EXTRA_ROOM, notice.roomId)
            .putExtra(EXTRA_ACCOUNT, account)
            .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP)
        val pending = PendingIntent.getActivity(
            context,
            notice.roomId.hashCode(),
            open,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val hidden = NotificationCompat.Builder(context, MESSAGES)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(context.getString(R.string.app_name))
            .setContentText(context.getString(R.string.notification_hidden))
            .build()
        return NotificationCompat.Builder(context, MESSAGES)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setStyle(style)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setPublicVersion(hidden)
            .setContentIntent(pending)
            .setAutoCancel(true)
            .setOnlyAlertOnce(false)
            .build()
    }

    private fun text(context: Context, message: NoticeItem): String {
        val line = message.text.lineSequence().firstOrNull { it.isNotBlank() }?.trim()
        return when {
            line != null -> line.take(MAX_TEXT)
            message.attachments.isNotEmpty() -> context.getString(R.string.preview_attachment)
            else -> ""
        }
    }

    private const val MESSAGE_ID = 2
    private const val MAX_TEXT = 300
}

/** A room to open, from a tapped notification of [account]. */
data class PendingRoom(val account: String, val roomId: String)

/** The room of a tapped notification, until the chat takes it. */
class PendingRooms(private val session: StateFlow<SessionState>) {
    private val tapped = MutableStateFlow<PendingRoom?>(null)
    val pending: StateFlow<PendingRoom?> = tapped.asStateFlow()

    /**
     * A tapped notification; the activity is exported, so the values may come
     * from any app. Signed out, nobody's room can open. While the session is
     * being restored it is not known yet whose it will be, and [take] decides.
     */
    fun tap(account: String?, roomId: String?) {
        if (account == null || roomId == null || !isUuid(roomId)) return
        if (session.value is SessionState.SignedOut) return
        tapped.value = PendingRoom(account, roomId)
    }

    /** The pending room if it belongs to [account]; a room of another one is dropped. */
    fun take(account: String): String? =
        tapped.getAndUpdate { null }?.takeIf { it.account == account }?.roomId

    fun clear() {
        tapped.value = null
    }
}

/** The system's notifications as the board that [Notifier] writes on. */
class SystemNoticeBoard(private val context: Context) : NoticeBoard {
    override fun post(account: String, notices: List<RoomNotice>) = Notifications.post(context, account, notices)

    override fun shownRooms(): Set<String> = Notifications.shownRooms(context)

    override fun cancel(roomId: String) = Notifications.cancel(context, roomId)
}
