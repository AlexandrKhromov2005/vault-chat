package io.github.alexandrkhromov2005.vaultchat

import android.content.Context
import androidx.core.app.NotificationCompat
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.OutOfQuotaPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull
import java.util.concurrent.TimeUnit

/**
 * Synchronises the current session when the app is not on screen and shows
 * what is new (docs/android/push.md): at once when a push arrives, and every
 * 15 minutes as a fallback for lost pushes or a build without push.
 */
class SyncWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val container = (applicationContext as VaultChatApplication).container
        val done = try {
            withTimeoutOrNull(BUDGET_MS) {
                val chat = container.chats.awaitCurrent() ?: return@withTimeoutOrNull true
                chat.retain().use {
                    val failure = chat.sync.syncNow()
                    chat.notifier.check()
                    failure == null
                }
            } ?: false
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            false
        }
        return if (done || tags.contains(PERIODIC)) Result.success() else Result.retry()
    }

    // An expedited job below Android 12 runs as a foreground service.
    override suspend fun getForegroundInfo(): ForegroundInfo = ForegroundInfo(
        Notifications.SYNC_ID,
        NotificationCompat.Builder(applicationContext, Notifications.SYNC)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(applicationContext.getString(R.string.notification_syncing))
            .setPriority(NotificationCompat.PRIORITY_MIN)
            .build(),
    )

    companion object {
        private const val NOW = "sync-now"
        private const val PERIODIC = "sync-periodic"

        // WorkManager stops a job after 10 minutes; a sync takes seconds.
        private const val BUDGET_MS = 3 * 60 * 1000L

        private val network = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

        /** Synchronises at once, for a push; a sync under way is replaced, since it may predate the message. */
        fun runNow(context: Context) {
            val request = OneTimeWorkRequestBuilder<SyncWorker>()
                .setConstraints(network)
                .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
                .build()
            WorkManager.getInstance(context).enqueueUniqueWork(NOW, ExistingWorkPolicy.REPLACE, request)
        }

        /** Synchronises every 15 minutes, the shortest period WorkManager allows. */
        fun schedulePeriodic(context: Context) {
            val request = PeriodicWorkRequestBuilder<SyncWorker>(15, TimeUnit.MINUTES)
                .setConstraints(network)
                .addTag(PERIODIC)
                .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(PERIODIC, ExistingPeriodicWorkPolicy.KEEP, request)
        }
    }
}
