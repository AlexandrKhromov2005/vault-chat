package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.FileDto
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.ResponseBody
import okhttp3.ResponseBody.Companion.asResponseBody
import okio.Buffer
import okio.BufferedSource
import okio.Source
import okio.Timeout
import okio.buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import retrofit2.Response
import java.io.IOException
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
class AttachmentsTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val server = FakeChatServer(me)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val gate = RateGate(System::currentTimeMillis)
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao
    private lateinit var cacheDir: java.io.File
    private lateinit var roomId: String

    @Before
    fun setUp(): Unit = runBlocking {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        dao = database.dao()
        cacheDir = temporary.newFolder("cache")
        roomId = server.addChannel(1)
        dao.storeRoomList(listOf(RoomEntity(roomId, RoomEntity.CHANNEL, "room", me, false, 0, null, null, 0)), complete = true)
    }

    @After
    fun tearDown() {
        scope.cancel()
        database.close()
    }

    private fun attachments(
        requests: ChatRequests = server.requests,
        budget: RequestBudget = RequestBudget(6_000, 100, System::currentTimeMillis),
        cache: AttachmentCache = AttachmentCache(cacheDir),
    ) = Attachments(dao, requests, gate, cache, Staging(temporary.root.resolve("staging")), scope, budget)

    private suspend fun known(files: Attachments, fileId: String): FileEntity =
        withTimeout(5_000) { (files.metadata(roomId, fileId).first { it is FileState.Known } as FileState.Known).file }

    private suspend fun settled(files: Attachments, fileId: String): FileState =
        withTimeout(5_000) { files.metadata(roomId, fileId).first { it != FileState.Loading } }

    private suspend fun downloaded(files: Attachments, file: FileEntity): ContentState =
        withTimeout(5_000) { files.content(file).first { it !is ContentState.Loading } }

    private fun calls(prefix: String) = server.calls().count { it.startsWith(prefix) }

    @Test
    fun metadataIsReadOnceAndStored() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val files = attachments()
        // Two bubbles showing the same file share one request.
        val both = List(2) { async { known(files, dto.fileId) } }.awaitAll()
        assertEquals(dto.filename, both[0].name)
        assertEquals(both[0], both[1])
        assertEquals(1, calls("file "))
        assertEquals(dto.filename, dao.fileRow(dto.fileId)!!.name)

        known(attachments(), dto.fileId)
        assertEquals(1, calls("file "))
    }

    @Test
    fun metadataOfAnotherRoomOrWithABogusSizeIsNotStored() = runBlocking {
        val other = server.addChannel(2)
        val foreign = server.addFile(other, 1, "x".toByteArray())
        val files = attachments()
        // The message is in roomId, the server says the file is in another room.
        assertTrue(settled(files, foreign.fileId) is FileState.Failed)
        assertNull(dao.fileRow(foreign.fileId))

        val dto = server.addFile(roomId, 2, "hello".toByteArray())
        server.fileReply = { it.copy(size = MAX_FILE_BYTES + 1) }
        assertTrue(settled(files, dto.fileId) is FileState.Failed)
        assertNull(dao.fileRow(dto.fileId))
    }

    @Test
    fun anUnavailableFileIsNotAskedForAgainUntilTheUserTaps() = runBlocking {
        val files = attachments()
        val missing = server.id(5, 9)
        assertEquals(FileState.Unavailable, settled(files, missing))
        assertEquals(FileState.Unavailable, settled(files, missing))
        // A new network retries failures, not files the server denied.
        files.retry()
        assertEquals(FileState.Unavailable, settled(files, missing))
        assertEquals(1, calls("file "))

        server.files[missing] = FileDto(missing, roomId, me, "late.txt", "text/plain", 1, "2026-10-30T12:00:00Z") to byteArrayOf(1)
        val watching = async { known(files, missing) }
        delay(100)
        files.retry(missing)
        assertEquals("late.txt", watching.await().name)
        assertEquals(2, calls("file "))
    }

    @Test
    fun aFailedLoadIsTriedAgainOnRetry() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        server.intercept = { if (it.startsWith("file ")) 503 else null }
        val files = attachments()
        val state = settled(files, dto.fileId)
        assertEquals(503, (state as FileState.Failed).error.status)

        server.intercept = { null }
        val watching = async { known(files, dto.fileId) }
        delay(100)
        files.retry()
        assertEquals(dto.filename, watching.await().name)
    }

    @Test
    fun aRateLimitPausesAndRepeats() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val first = AtomicInteger()
        server.intercept = { if (it.startsWith("file ") && first.getAndIncrement() == 0) 429 else null }
        val files = attachments()
        val start = System.currentTimeMillis()
        assertEquals(dto.filename, known(files, dto.fileId).name)
        assertEquals(2, calls("file "))
        // The repeat waited for the pause (the fake's Retry-After: 1), which sync and sending share.
        assertTrue(System.currentTimeMillis() - start >= 1_000)
    }

    @Test
    fun contentIsDownloadedIntoTheCacheOnce() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val files = attachments()
        val file = known(files, dto.fileId)
        val ready = downloaded(files, file) as ContentState.Ready
        assertEquals("hello", ready.file.readText())
        assertEquals("file 1.txt", ready.file.name)
        assertEquals(ready, downloaded(files, file))
        assertEquals(1, calls("content "))
    }

    @Test
    fun contentThatIsNotTheFileIsDiscarded() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val files = attachments()
        val file = known(files, dto.fileId)

        server.contentReply = { "hello, and more".toByteArray() }
        assertTrue(downloaded(files, file) is ContentState.Failed)
        server.contentReply = { "hell".toByteArray() }
        assertTrue(downloaded(files, file) is ContentState.Failed)
        // Nothing is left in the cache, not even a part.
        assertEquals(emptyList<String>(), cacheDir.resolve(dto.fileId).list().orEmpty().toList())

        server.contentReply = { it }
        assertTrue(downloaded(files, file) is ContentState.Ready)
    }

    @Test
    fun contentWithoutALengthIsCheckedAsItArrives() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val file = attachments().let { known(it, dto.fileId) }
        // As a chunked answer arrives: no Content-Length to compare first.
        val requests = object : ChatRequests {
            override suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T =
                server.requests.send(request)

            @Suppress("UNCHECKED_CAST")
            override suspend fun <T : Any> transfer(request: suspend GatewayApi.(authorization: String) -> Response<T>): T {
                val body = server.requests.transfer(request) as ResponseBody
                return body.source().asResponseBody(body.contentType(), -1) as T
            }
        }
        val files = attachments(requests)
        server.contentReply = { ByteArray(100_000) }
        assertTrue(downloaded(files, file) is ContentState.Failed)
        assertEquals(emptyList<String>(), cacheDir.resolve(dto.fileId).list().orEmpty().toList())
        server.contentReply = { "hell".toByteArray() }
        assertTrue(downloaded(files, file) is ContentState.Failed)
    }

    @Test
    fun contentThatDoesNotEndIsCutAtTheFilesSize() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val file = attachments().let { known(it, dto.fileId) }
        val endless = object : Source {
            override fun read(sink: Buffer, byteCount: Long): Long {
                sink.write(ByteArray(byteCount.toInt()))
                return byteCount
            }

            override fun timeout() = Timeout.NONE

            override fun close() = Unit
        }
        val requests = object : ChatRequests {
            override suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T =
                server.requests.send(request)

            @Suppress("UNCHECKED_CAST")
            override suspend fun <T : Any> transfer(request: suspend GatewayApi.(authorization: String) -> Response<T>): T {
                val body = server.requests.transfer(request) as ResponseBody
                return endless.buffer().asResponseBody(body.contentType(), -1) as T
            }
        }
        assertTrue(downloaded(attachments(requests), file) is ContentState.Failed)
        assertEquals(emptyList<String>(), cacheDir.resolve(dto.fileId).list().orEmpty().toList())
    }

    @Test
    fun contentOfAFileTheUserCannotReadIsUnavailable() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val files = attachments()
        val file = known(files, dto.fileId)
        server.removeRoom(roomId)
        assertEquals(ContentState.Unavailable, downloaded(files, file))
    }

    @Test
    fun aBrokenDownloadFailsAndIsTriedAgain() = runBlocking {
        val dto = server.addFile(roomId, 1, ByteArray(300_000) { it.toByte() })
        val file = attachments().let { known(it, dto.fileId) }
        val broken = AtomicInteger()
        val requests = object : ChatRequests {
            override suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T =
                server.requests.send(request)

            @Suppress("UNCHECKED_CAST")
            override suspend fun <T : Any> transfer(request: suspend GatewayApi.(authorization: String) -> Response<T>): T {
                val body = server.requests.transfer(request) as ResponseBody
                if (broken.getAndIncrement() > 0) return body as T
                // Half of the content arrives, then the connection breaks.
                val half = Buffer().write(body.bytes().copyOf(150_000))
                return failingAfter(half).asResponseBody(body.contentType(), body.contentLength()) as T
            }
        }
        val files = attachments(requests)
        val state = downloaded(files, file)
        assertEquals(ApiException.NETWORK, (state as ContentState.Failed).error.code)
        val watching = async { downloaded(files, file) }
        delay(100)
        files.retry()
        assertTrue(watching.await() is ContentState.Ready)
    }

    @Test
    fun aDownloadStopsWhenNobodyWaitsForIt() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        val file = attachments().let { known(it, dto.fileId) }
        val started = CompletableDeferred<Unit>()
        val stopped = CompletableDeferred<Unit>()
        val requests = object : ChatRequests {
            override suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T =
                server.requests.send(request)

            override suspend fun <T : Any> transfer(request: suspend GatewayApi.(authorization: String) -> Response<T>): T {
                started.complete(Unit)
                try {
                    delay(Long.MAX_VALUE)
                } finally {
                    stopped.complete(Unit)
                }
                error("never")
            }
        }
        val files = attachments(requests)
        val watching = launch { files.content(file).collect { } }
        withTimeout(5_000) { started.await() }
        watching.cancelAndJoin()
        withTimeout(5_000) { stopped.await() }
    }

    @Test
    fun loadsKeepToTheirConcurrency() = runBlocking {
        val dtos = (1..6).map { server.addFile(roomId, it, "file $it".toByteArray()) }
        val running = AtomicInteger()
        val most = AtomicInteger()
        val requests = object : ChatRequests {
            override suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T {
                most.accumulateAndGet(running.incrementAndGet(), ::maxOf)
                try {
                    delay(50)
                    return server.requests.send(request)
                } finally {
                    running.decrementAndGet()
                }
            }
        }
        val files = attachments(requests)
        dtos.map { async { known(files, it.fileId) } }.awaitAll()
        assertEquals(Attachments.CONCURRENT_LOADS, most.get())
    }

    @Test
    fun loadsKeepToTheirBudget() = runBlocking {
        val dtos = (1..4).map { server.addFile(roomId, it, "file $it".toByteArray()) }
        // A burst of 1: one load goes at once, one more in debt, then one a second.
        val files = attachments(budget = RequestBudget(60, 1, System::currentTimeMillis))
        val loads = dtos.map { async { settled(files, it.fileId) } }
        withTimeout(5_000) { while (calls("file ") < 2) delay(10) }
        delay(300)
        assertEquals(2, calls("file "))
        loads.awaitAll()
        assertEquals(4, calls("file "))
    }

    @Test
    fun aFileOfARoomThatIsGoneIsNotStored() = runBlocking {
        val dto = server.addFile(roomId, 1, "hello".toByteArray())
        dao.deleteRooms(listOf(roomId))
        assertEquals(FileState.Unavailable, settled(attachments(), dto.fileId))
        assertNull(dao.fileRow(dto.fileId))
    }

    private fun failingAfter(content: Buffer): BufferedSource = object : Source {
        override fun read(sink: Buffer, byteCount: Long): Long {
            if (content.exhausted()) throw IOException("connection reset")
            return content.read(sink, byteCount)
        }

        override fun timeout() = Timeout.NONE

        override fun close() = Unit
    }.buffer()
}
