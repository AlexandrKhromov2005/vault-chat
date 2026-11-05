package io.github.alexandrkhromov2005.vaultchat

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import io.github.alexandrkhromov2005.vaultchat.core.chat.sendQueuedMessages

/**
 * Sends queued messages when the app is not on screen (docs/android/send.md).
 * One job at a time, started once a network is available. It sends through the
 * current session's Outbox, the same one the screens use, so a message is
 * never sent twice at once. Not expedited: below Android 12 that needs a
 * foreground notification, and a job with a network constraint runs within
 * minutes anyway.
 */
class OutboxWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val container = (applicationContext as VaultChatApplication).container
        return if (sendQueuedMessages(container.chats, BUDGET_MS)) Result.success() else Result.retry()
    }

    companion object {
        internal const val NAME = "outbox"

        // WorkManager stops a job after 10 minutes.
        private const val BUDGET_MS = 8 * 60 * 1000L

        /**
         * Starts the job, replacing one that waits (for a network, or for its
         * backoff after retries) or runs: a running job may already have seen
         * an empty queue. Cancelling it is safe, since sending runs in the
         * session's scope and repeats with the same key.
         */
        fun schedule(context: Context) {
            val request = OneTimeWorkRequestBuilder<OutboxWorker>()
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .build()
            WorkManager.getInstance(context).enqueueUniqueWork(NAME, ExistingWorkPolicy.REPLACE, request)
        }
    }
}
