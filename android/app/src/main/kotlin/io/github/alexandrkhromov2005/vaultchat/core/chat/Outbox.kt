package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingFileEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.FileDto
import io.github.alexandrkhromov2005.vaultchat.core.net.FileUpload
import io.github.alexandrkhromov2005.vaultchat.core.net.SendMessageRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.async
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withTimeoutOrNull
import java.io.InterruptedIOException
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

/** How far the upload of a queued message's file has come. */
data class UploadProgress(val position: Int, val sent: Long, val total: Long)

/**
 * The outgoing queue of a session (docs/android/send.md). Messages are stored
 * first and sent afterwards: in order within a room, one at a time, at most
 * [CONCURRENT_SENDS] rooms at once. A send whose outcome is unknown is
 * repeated with the same key, which the server answers with the original
 * message, so a message is never stored twice.
 *
 * A message's files are uploaded first, one at a time for the whole session
 * (docs/android/attachments.md). An upload is repeated by itself only when
 * the server cannot have stored the file; a file uploaded once keeps its ID,
 * so the files under a key never change.
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
    /** Staged copies of the files of queued messages. */
    private val staging: Staging? = null,
    /** Takes the metadata and the copy of an uploaded file. */
    private val attachments: Attachments? = null,
    /** Uploads keep to the files' budget, which they share with downloads. */
    private val uploadBudget: RequestBudget = budget,
) {
    private val gate = sync.gate
    private val permits = Semaphore(CONCURRENT_SENDS)
    private val uploadPermit = Semaphore(1)

    // Running uploads by message key, so that Cancel can stop one.
    private val uploads = ConcurrentHashMap<String, Deferred<FileDto>>()

    private val _progress = MutableStateFlow<Map<String, UploadProgress>>(emptyMap())

    /** Uploads under way, by message key. */
    val progress: StateFlow<Map<String, UploadProgress>> = _progress

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

    /** The files of the room's queued messages. */
    fun outgoingFiles(roomId: String): Flow<List<OutgoingFileEntity>> = dao.outgoingFiles(roomId)

    /**
     * Queues [text] for [roomId] unchanged, with [files] staged for it.
     * Returns whether it was queued: not if the server would reject the
     * message (MessageText; empty text only with files, at most
     * [MAX_ATTACHMENTS]) or the room is no longer stored, so that the
     * composer keeps it. Once queued, the staged copies belong to the queue.
     */
    suspend fun send(roomId: String, text: String, files: List<StagedFile> = emptyList()): Boolean {
        if (files.size > MAX_ATTACHMENTS) return false
        if (!(text.isEmpty() && files.isNotEmpty()) && MessageText.check(text) != null) return false
        val row = OutgoingEntity(
            clientMessageId = newId(),
            roomId = roomId,
            senderId = me,
            position = 0,
            text = text,
            createdAt = nowMicros(),
            state = OutgoingEntity.QUEUED,
        )
        val stored = files.mapIndexed { index, file ->
            OutgoingFileEntity(row.clientMessageId, index, file.name, file.kind.mime, file.size, file.staged)
        }
        if (!dao.enqueue(row, stored)) return false
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
        staging?.delete(dao.deleteFailed(clientMessageId))
    }

    /**
     * Deletes a queued message while one of its files is not uploaded: it
     * has never been sent, since a message goes only with all its files.
     * A running upload of it stops. Returns whether it was deleted.
     */
    suspend fun cancel(clientMessageId: String): Boolean {
        val staged = dao.deleteUnsent(clientMessageId) ?: return false
        uploads[clientMessageId]?.cancel()
        staging?.delete(staged)
        return true
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
        val files = dao.filesOf(row.clientMessageId)
        files.firstOrNull { it.fileId == null }?.let { file ->
            // An earlier attempt may have stored it: the user decides.
            if (file.maybeStored) return fail(row, OutgoingEntity.ERROR_UPLOAD)
            uploadBudget.take()
            return uploadPermit.withPermit {
                awaitPause()
                upload(row, file)
            }
        }
        if (repeat) budget.take() else budget.takeWithinDebt()
        return permits.withPermit {
            // A 429 may have started a pause while this room waited for a permit.
            awaitPause()
            attempt(row, files.map { it.fileId!! })
        }
    }

    private suspend fun awaitPause() {
        while (gate.remaining() > 0) delay(gate.remaining())
    }

    private enum class Outcome { EMPTY, DONE, RETRY, PAUSED }

    private suspend fun attempt(row: OutgoingEntity, fileIds: List<String>): Outcome {
        val request = SendMessageRequest(row.clientMessageId, row.text, fileIds.ifEmpty { null })
        val message = try {
            requests.send { sendMessage(it, row.roomId, request) }
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
            message.toEntity(row.roomId).also { it.check(row, fileIds) }
        } catch (e: ApiException) {
            // If the server stored it after all, sync brings it and resolves the row.
            return fail(row, OutgoingEntity.ERROR_INVALID)
        }
        // Applied first: if that fails, the row is still queued and the send is
        // repeated with the same key. Marked sent first, a failure here would
        // leave a sent row that only a later sync resolves.
        sync.receive(stored)
        dao.markSent(row.roomId, row.clientMessageId, stored.sequence)
        return Outcome.DONE
    }

    /** The answer must be the queued message itself, with its files in order. */
    private fun MessageEntity.check(row: OutgoingEntity, fileIds: List<String>) {
        if (senderId != me || clientMessageId != row.clientMessageId || text != row.text || attachmentIds != fileIds) {
            throw invalid("not the queued message")
        }
    }

    /**
     * Uploads one file of a queued message. Whether the server may have
     * stored it is kept on disk from just before the multipart body's
     * closing boundary leaves until an answer arrives (FileUpload), so that
     * a process killed meanwhile does not upload it again by itself.
     */
    private suspend fun upload(row: OutgoingEntity, file: OutgoingFileEntity): Outcome {
        val staged = staging ?: return fail(row, OutgoingEntity.ERROR_FILE)
        val local = staged.file(file.staged)
        if (!local.isFile || local.length() != file.size) return fail(row, OutgoingEntity.ERROR_FILE)
        val key = row.clientMessageId
        val body = FileUpload(
            local,
            file.name,
            onStart = { dao.setMaybeStored(key, file.position, false) },
            beforeEnd = { dao.setMaybeStored(key, file.position, true) },
            onProgress = { sent -> _progress.update { it + (key to UploadProgress(file.position, sent, file.size)) } },
        )
        // Not a child, so that Cancel stops the upload without stopping the room's sender.
        val call = scope.async { requests.transfer { uploadFile(it, row.roomId, body) } }
        uploads[key] = call
        val answer = try {
            call.await()
        } catch (e: CancellationException) {
            currentCoroutineContext().ensureActive()
            // Cancelled by the user: the message is gone.
            return Outcome.DONE
        } catch (e: ApiException) {
            if (e.status != 0) dao.setMaybeStored(key, file.position, false)
            return when {
                e.status == 429 -> {
                    gate.pause(e.retryAfterSeconds)
                    Outcome.PAUSED
                }
                // The connection broke: stored only if the closing boundary had begun to leave.
                e.status == 0 && e.code == ApiException.NETWORK -> when {
                    body.endStarted -> fail(row, OutgoingEntity.ERROR_UPLOAD)
                    body.started && e.cause is InterruptedIOException -> ranOutOfTime(row, file)
                    else -> Outcome.RETRY
                }
                // The session renewed its token and repeated once; Media is busy or down.
                e.status == 401 || e.status == 503 -> Outcome.RETRY
                e.status == 400 || e.status == 413 || e.status == 415 -> fail(row, OutgoingEntity.ERROR_FILE)
                e.status == 403 || e.status == 404 -> fail(row, OutgoingEntity.ERROR_NO_ACCESS)
                // A timeout, a failure inside the server or an answer that is not a file: unknown.
                else -> fail(row, OutgoingEntity.ERROR_UPLOAD)
            }
        } finally {
            uploads.remove(key, call)
            call.cancel()
            _progress.update { it - key }
        }
        val entity = try {
            answer.toUploaded(row.roomId, me, file.size)
        } catch (e: ApiException) {
            dao.setMaybeStored(key, file.position, false)
            return fail(row, OutgoingEntity.ERROR_UPLOAD)
        }
        // False if the message was cancelled meanwhile.
        if (dao.setUploaded(key, file.position, entity.fileId)) attachments?.adopt(entity, local)
        return Outcome.DONE
    }

    /**
     * An attempt that ran out of time while the file was being sent: a repeat
     * starts from the first byte and may do no better on this network, so
     * the transfer budget ends in an outcome after a few. A connection that
     * could not be opened sends nothing and does not count.
     */
    private suspend fun ranOutOfTime(row: OutgoingEntity, file: OutgoingFileEntity): Outcome {
        val timeouts = file.timeouts + 1
        dao.setUploadTimeouts(row.clientMessageId, file.position, timeouts)
        return if (timeouts >= MAX_UPLOAD_TIMEOUTS) fail(row, OutgoingEntity.ERROR_TOO_SLOW) else Outcome.RETRY
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

        /** Attempts of a file's upload that may run out of time before its message fails. */
        const val MAX_UPLOAD_TIMEOUTS = 3

        private const val RESTART_AFTER_FAILURE_MS = 1_000L
    }
}
