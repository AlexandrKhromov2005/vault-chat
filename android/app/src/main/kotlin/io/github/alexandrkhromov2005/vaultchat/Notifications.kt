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
import io.github.alexandrkhromov2005.vaultchat.core.chat.RoomNotice
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.NoticeItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity

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

    fun post(context: Context, notices: List<RoomNotice>) {
        val manager = NotificationManagerCompat.from(context)
        if (!manager.areNotificationsEnabled()) return
        for (notice in notices) {
            try {
                manager.notify(notice.roomId, MESSAGE_ID, build(context, notice))
            } catch (e: SecurityException) {
                // The permission was taken back meanwhile.
                return
            }
        }
    }

    /** Removes the notification of a room the user has opened. */
    fun cancel(context: Context, roomId: String) {
        NotificationManagerCompat.from(context).cancel(roomId, MESSAGE_ID)
    }

    private fun build(context: Context, notice: RoomNotice): android.app.Notification {
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
