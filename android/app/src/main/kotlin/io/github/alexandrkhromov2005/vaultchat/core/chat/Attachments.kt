package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.shareIn
import kotlinx.coroutines.flow.transformLatest
import kotlinx.coroutines.flow.transformWhile
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.job
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap

/** What is known of a file's metadata. */
sealed interface FileState {
    data object Loading : FileState

    /** Not readable for this user (`404`): gone, or the room is no longer theirs. */
    data object Unavailable : FileState

    /** Loading failed; it is tried again on [Attachments.retry] or a new network. */
    data class Failed(val error: ApiException) : FileState

    data class Known(val file: FileEntity) : FileState
}

/** What is known of a file's content. */
sealed interface ContentState {
    data class Loading(val received: Long, val total: Long) : ContentState

    /** Not readable for this user (`404`). */
    data object Unavailable : ContentState

    /** The download failed, or what arrived was not the file; tried again like [FileState.Failed]. */
    data class Failed(val error: ApiException) : ContentState

    data class Ready(val file: File) : ContentState
}

/** What screens need to show files; [Attachments] in the app. */
interface FileSource {
    fun metadata(roomId: String, fileId: String): Flow<FileState>

    fun content(file: FileEntity): Flow<ContentState>

    fun retry(fileId: String? = null)

    /**
     * What this process already knows of [fileId], to show at once instead
     * of a frame of loading while [metadata] reads the database.
     */
    fun known(fileId: String): FileState.Known? = null

    /** Like [known], for the content of [file]: its copy, if one was ready. */
    fun ready(file: FileEntity): ContentState.Ready? = null

    /** The local copy of a picked file of a queued message. */
    fun staged(name: String): File
}

/**
 * Metadata and content of the session's files (docs/android/attachments.md).
 *
 * Metadata is stored once read. Content is downloaded into the [cache] while
 * someone collects it, so a picture scrolled past stops downloading. Both
 * are loaded once however many collect them, at most [CONCURRENT_LOADS] at a
 * time, within the files' own request [budget] and the shared `429` pause.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class Attachments(
    private val dao: ChatDao,
    private val requests: ChatRequests,
    private val gate: RateGate,
    private val cache: AttachmentCache,
    private val staging: Staging,
    private val scope: CoroutineScope,
    private val budget: RequestBudget,
) : FileSource {
    private val slots = Semaphore(CONCURRENT_LOADS)
    private val metadataLoads = ConcurrentHashMap<String, Deferred<FileState?>>()
    private val downloads = ConcurrentHashMap<String, SharedFlow<ContentState>>()

    // Files the server answered 404 for; not asked again until the user taps one.
    private val unavailable = ConcurrentHashMap.newKeySet<String>()

    // What was already shown, so that a bubble shown again, such as an own
    // message moving from the queue to the history, keeps its size. The
    // flows still check the database and the cache.
    private val knownFiles = ConcurrentHashMap<String, FileEntity>()
    private val readyFiles = ConcurrentHashMap<String, File>()

    // Bumped to try failed loads again.
    private val retries = MutableStateFlow(0L)

    /** The metadata of [fileId], a file of a message in [roomId], loaded if it is not stored. */
    override fun metadata(roomId: String, fileId: String): Flow<FileState> = dao.file(fileId).transformLatest { row ->
        if (row != null) {
            knownFiles[fileId] = row
            emit(FileState.Known(row))
            return@transformLatest
        }
        while (true) {
            val attempt = retries.value
            emit(FileState.Loading)
            // Null once stored: the row arrives through the database.
            val state = loadMetadata(roomId, fileId) ?: return@transformLatest
            emit(state)
            retries.first { it != attempt }
        }
    }.distinctUntilChanged()

    /**
     * The content of [file], downloaded if it is not in the cache. The
     * download stops when nobody collects any more.
     */
    override fun content(file: FileEntity): Flow<ContentState> = flow {
        while (true) {
            val attempt = retries.value
            val cached = withContext(Dispatchers.IO) { cache.get(file.fileId, localName(file), file.size) }
            if (cached != null) {
                readyFiles[file.fileId] = cached
                emit(ContentState.Ready(cached))
                return@flow
            }
            if (file.fileId in unavailable) {
                emit(ContentState.Unavailable)
            } else {
                val shared = downloads.computeIfAbsent(file.fileId) {
                    download(file).shareIn(scope, SharingStarted.WhileSubscribed(replayExpirationMillis = 0), replay = 1)
                }
                var last: ContentState? = null
                shared.transformWhile { state ->
                    emit(state)
                    state is ContentState.Loading
                }.collect {
                    last = it
                    emit(it)
                }
                downloads.remove(file.fileId, shared)
                (last as? ContentState.Ready)?.let {
                    readyFiles[file.fileId] = it.file
                    return@flow
                }
            }
            retries.first { it != attempt }
        }
    }.distinctUntilChanged()

    override fun staged(name: String): File = staging.file(name)

    override fun known(fileId: String): FileState.Known? = knownFiles[fileId]?.let { FileState.Known(it) }

    override fun ready(file: FileEntity): ContentState.Ready? =
        readyFiles[file.fileId]?.let { ContentState.Ready(it) }

    /** Tries failed loads again; a tap on an unavailable file asks the server again too. */
    override fun retry(fileId: String?) {
        if (fileId != null) unavailable.remove(fileId)
        retries.update { it + 1 }
    }

    /** Stores the metadata of a file this user uploaded and moves its [local] copy into the cache. */
    suspend fun adopt(file: FileEntity, local: File) {
        dao.storeFile(file)
        withContext(Dispatchers.IO) {
            cache.adopt(file.fileId, localName(file), local)
            cache.trim(keep = file.fileId)
        }
    }

    private suspend fun loadMetadata(roomId: String, fileId: String): FileState? {
        if (fileId in unavailable) return FileState.Unavailable
        // Started once it is in the map, so that it cannot leave the map before it is put there.
        val load = metadataLoads.computeIfAbsent(fileId) {
            scope.async(start = CoroutineStart.LAZY) {
                try {
                    fetchMetadata(roomId, fileId)
                } finally {
                    metadataLoads.remove(fileId, coroutineContext.job)
                }
            }
        }
        load.start()
        return load.await()
    }

    private suspend fun fetchMetadata(roomId: String, fileId: String): FileState? {
        val answer = try {
            request { requests.send { file(it, fileId) } }
        } catch (e: ApiException) {
            return if (failure(fileId, e)) FileState.Unavailable else FileState.Failed(e)
        }
        val entity = try {
            answer.toEntity(fileId, roomId)
        } catch (e: ApiException) {
            return FileState.Failed(e)
        }
        // A room that is gone takes its files along; nothing to show then.
        return if (dao.storeFile(entity)) null else FileState.Unavailable
    }

    private fun download(file: FileEntity): Flow<ContentState> = channelFlow {
        send(ContentState.Loading(0, file.size))
        val state = try {
            val body = request { requests.transfer { fileContent(it, file.fileId) } }
            body.use {
                val stored = cache.put(file.fileId, localName(file)) { part ->
                    val source = it.byteStream()
                    part.outputStream().use { out ->
                        val buffer = ByteArray(BUFFER_BYTES)
                        var total = 0L
                        var reported = 0L
                        while (true) {
                            val read = source.read(buffer)
                            if (read < 0) break
                            total += read
                            // Stops at once, so that content without an end cannot fill the disk.
                            if (total > file.size) throw invalid("content larger than the file")
                            out.write(buffer, 0, read)
                            if (total - reported >= PROGRESS_STEP_BYTES) {
                                reported = total
                                trySend(ContentState.Loading(total, file.size))
                            }
                        }
                        if (total != file.size) throw invalid("content shorter than the file")
                    }
                }
                cache.trim(keep = file.fileId)
                ContentState.Ready(stored)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            if (failure(file.fileId, e)) ContentState.Unavailable else ContentState.Failed(e)
        } catch (e: IOException) {
            // The connection broke while the body was read, or the disk failed.
            ContentState.Failed(ApiException(0, ApiException.NETWORK, "download failed", cause = e))
        }
        send(state)
    }.flowOn(Dispatchers.IO)

    /** Runs a request within the budget and the `429` pause, at most [CONCURRENT_LOADS] at a time. */
    private suspend fun <T> request(block: suspend () -> T): T {
        while (true) {
            val answer = slots.withPermit {
                while (gate.remaining() > 0) delay(gate.remaining())
                budget.takeWithinDebt()
                try {
                    Result.success(block())
                } catch (e: ApiException) {
                    if (e.status != 429) throw e
                    gate.pause(e.retryAfterSeconds)
                    null
                }
            }
            if (answer != null) return answer.getOrThrow()
        }
    }

    /** Whether [e] means the file is not readable for this user; remembers it if so. */
    private fun failure(fileId: String, e: ApiException): Boolean {
        val gone = e.status == 400 || e.status == 403 || e.status == 404
        if (gone) unavailable += fileId
        return gone
    }

    private fun localName(file: FileEntity) = FileNames.local(file.name, FileKind.of(file.contentType))

    companion object {
        const val CONCURRENT_LOADS = 2

        // With sync's 60/min and sending's 30/min, the Gateway's 120/min.
        const val REQUESTS_PER_MINUTE = 30
        const val REQUEST_BURST = 5

        private const val BUFFER_BYTES = 64 * 1024
        private const val PROGRESS_STEP_BYTES = 256L * 1024
    }
}
