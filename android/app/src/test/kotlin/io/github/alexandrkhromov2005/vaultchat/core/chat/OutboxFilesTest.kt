package io.github.alexandrkhromov2005.vaultchat.core.chat

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDao
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabase
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingFileEntity
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okio.Buffer
import okio.ByteString.Companion.encodeUtf8
import okio.ForwardingSink
import okio.Sink
import okio.blackholeSink
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.IOException
import java.io.InterruptedIOException
import java.net.SocketTimeoutException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

/** Messages with files (docs/android/attachments.md, "Sending"). */
@RunWith(RobolectricTestRunner::class)
class OutboxFilesTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val server = FakeChatServer(me)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default + CoroutineExceptionHandler { _, _ -> })
    private val network = MutableStateFlow<Any?>(Unit)
    private val keys = AtomicInteger()
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao
    private lateinit var sync: ChatSync
    private lateinit var staging: Staging
    private lateinit var cache: AttachmentCache
    private lateinit var attachments: Attachments

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java).build()
        dao = database.dao()
        sync = ChatSync(dao, me, server.requests, scope, System::currentTimeMillis)
        staging = Staging(temporary.newFolder("staging"))
        cache = AttachmentCache(temporary.newFolder("cache"))
        attachments = Attachments(dao, server.requests, sync.gate, cache, staging, scope, RequestBudget(6_000, 100, System::currentTimeMillis))
    }

    @After
    fun tearDown() {
        scope.cancel()
        database.close()
    }

    private fun key(n: Int) = server.id(4, n)

    private fun outbox(on: CoroutineScope = scope, delays: () -> Backoff = { Backoff(20, 100) }) = Outbox(
        dao, me, server.requests, sync, on, network,
        newId = { key(keys.incrementAndGet()) },
        backoff = delays,
        staging = staging,
        attachments = attachments,
        uploadBudget = RequestBudget(6_000, 100, System::currentTimeMillis),
    )

    private suspend fun eventually(condition: suspend () -> Boolean) {
        withTimeout(5_000) { while (!condition()) delay(10) }
    }

    private suspend fun loadedChannel(n: Int): String {
        val roomId = server.addChannel(n)
        sync.syncNow()
        return roomId
    }

    private fun stage(name: String, content: ByteArray): StagedFile =
        (staging.stage(name, content.inputStream()) as Staging.Result.Staged).file

    private fun text(name: String, content: String = "content of $name") = stage(name, content.toByteArray())

    private val png = byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3)

    private fun uploads() = server.calls().count { it.startsWith("upload ") }

    private suspend fun row(roomId: String) = dao.storedOutgoing(roomId).single()

    private fun stagedCopies() = temporary.root.resolve("staging").list().orEmpty().toList()

    /** A sink that breaks the connection after [bytes] bytes, once. */
    private fun breaksAfter(bytes: Long): () -> Sink {
        val armed = AtomicInteger(1)
        return {
            if (armed.getAndDecrement() <= 0) {
                blackholeSink()
            } else {
                object : ForwardingSink(blackholeSink()) {
                    private var written = 0L

                    override fun write(source: Buffer, byteCount: Long) {
                        if (written + byteCount > bytes) throw IOException("connection reset")
                        written += byteCount
                        super.write(source, byteCount)
                    }
                }
            }
        }
    }

    @Test
    fun aMessageWithFilesIsUploadedInOrderAndSentWithTheirIds() = runBlocking {
        val roomId = loadedChannel(1)
        val notes = text("notes.txt")
        val picture = stage("cat.png", png)
        assertTrue(outbox().send(roomId, "look", listOf(notes, picture)))

        eventually { dao.storedOutgoing(roomId).isEmpty() }
        val stored = server.uploads()
        assertEquals(listOf("notes.txt", "cat.png"), stored.map { it.filename })
        val message = server.sent(roomId).single()
        assertEquals("look", message.text)
        assertEquals(stored.map { it.fileId }, message.attachmentIds)
        // Shown without a download: the metadata is stored and the copies moved into the cache.
        for (file in stored) {
            val row = dao.fileRow(file.fileId)!!
            assertNotNull(cache.get(row.fileId, FileNames.local(row.name, FileKind.of(row.contentType)), row.size))
        }
        assertEquals(emptyList<String>(), stagedCopies())
        assertEquals(stored.map { it.fileId }, dao.storedMessages(roomId).single().attachmentIds)
        assertEquals(0, server.calls().count { it.startsWith("content ") })
    }

    @Test
    fun aMessageOfFilesAloneHasNoText() = runBlocking {
        val roomId = loadedChannel(1)
        val box = outbox()
        assertFalse(box.send(roomId, "   ", listOf(text("a.txt"))))
        assertFalse(box.send(roomId, ""))
        assertFalse(box.send(roomId, "six", (1..6).map { text("$it.txt") }))
        assertTrue(box.send(roomId, "", listOf(text("only.txt"))))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals("", server.sent(roomId).single().text)
    }

    @Test
    fun anUploadBrokenBeforeTheClosingBoundaryIsRepeated() = runBlocking {
        val roomId = loadedChannel(1)
        // Breaks in the middle of the file.
        server.uploadSink = breaksAfter(100)
        val file = stage("big.txt", ByteArray(10_000) { 'a'.code.toByte() })
        outbox().send(roomId, "big", listOf(file))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(2, uploads())
        assertEquals(1, server.uploads().size)
        assertEquals(server.uploads().map { it.fileId }, server.sent(roomId).single().attachmentIds)
    }

    @Test
    fun anUploadWhoseAnswerIsLostFailsWithoutARepeat() = runBlocking {
        val roomId = loadedChannel(1)
        server.afterUpload = { throw IOException("answer lost") }
        outbox().send(roomId, "lost", listOf(text("a.txt")))
        eventually { row(roomId).state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_UPLOAD, row(roomId).error)
        delay(300)
        assertEquals(1, uploads())
        // The file stayed staged, for Retry.
        assertEquals(1, stagedCopies().size)
    }

    @Test
    fun anUploadBrokenWithinTheClosingBoundaryFails() = runBlocking {
        val roomId = loadedChannel(1)
        val file = text("a.txt", "x".repeat(1000))
        // The file reaches the server whole; the closing boundary breaks off.
        server.uploadSink = {
            object : ForwardingSink(blackholeSink()) {
                override fun write(source: Buffer, byteCount: Long) {
                    if (source.indexOf("--\r\n".encodeUtf8()) >= 0) throw IOException("connection reset")
                    super.write(source, byteCount)
                }
            }
        }
        outbox().send(roomId, "edge", listOf(file))
        eventually { row(roomId).state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_UPLOAD, row(roomId).error)
        assertEquals(0, server.uploads().size)
    }

    @Test
    fun aKilledProcessDoesNotUploadAgainWhatMayBeStored() = runBlocking {
        val roomId = loadedChannel(1)
        val maybe = text("maybe.txt")
        val never = text("never.txt")
        // As a process killed during an upload leaves them.
        dao.enqueue(
            OutgoingEntity(key(91), roomId, me, 0, "after the closing boundary", 0, OutgoingEntity.QUEUED),
            listOf(OutgoingFileEntity(key(91), 0, maybe.name, maybe.kind.mime, maybe.size, maybe.staged, maybeStored = true)),
        )
        val other = loadedChannel(2)
        dao.enqueue(
            OutgoingEntity(key(92), other, me, 0, "before it", 0, OutgoingEntity.QUEUED),
            listOf(OutgoingFileEntity(key(92), 0, never.name, never.kind.mime, never.size, never.staged)),
        )
        outbox()
        eventually { row(roomId).state == OutgoingEntity.FAILED && dao.storedOutgoing(other).isEmpty() }
        assertEquals(OutgoingEntity.ERROR_UPLOAD, row(roomId).error)
        assertEquals(listOf("never.txt"), server.uploads().map { it.filename })
    }

    /** A connection on which writing runs out of time. */
    private fun timingOut(error: () -> IOException = { InterruptedIOException("timeout") }): () -> Sink = {
        object : ForwardingSink(blackholeSink()) {
            override fun write(source: Buffer, byteCount: Long) = throw error()
        }
    }

    @Test
    fun anUploadThatKeepsRunningOutOfTimeFails() = runBlocking {
        val roomId = loadedChannel(1)
        server.uploadSink = timingOut()
        val box = outbox()
        box.send(roomId, "slow", listOf(text("a.txt")))
        eventually { row(roomId).state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_TOO_SLOW, row(roomId).error)
        assertEquals(Outbox.MAX_UPLOAD_TIMEOUTS, uploads())
        delay(300)
        assertEquals(Outbox.MAX_UPLOAD_TIMEOUTS, uploads())
        // Other messages go on, and the file stays for Retry.
        box.send(loadedChannel(2), "next")
        eventually { server.sent(server.id(1, 2)).isNotEmpty() }
        assertEquals(1, stagedCopies().size)
    }

    @Test
    fun aStalledConnectionCountsAsRunningOutOfTime() = runBlocking {
        val roomId = loadedChannel(1)
        server.uploadSink = timingOut { SocketTimeoutException("timeout") }
        outbox().send(roomId, "stalled", listOf(text("a.txt")))
        eventually { row(roomId).state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_TOO_SLOW, row(roomId).error)
    }

    @Test
    fun attemptsThatRanOutOfTimeAreCountedAcrossProcesses() = runBlocking {
        val roomId = loadedChannel(1)
        server.uploadSink = timingOut()
        val process = CoroutineScope(SupervisorJob() + Dispatchers.Default + CoroutineExceptionHandler { _, _ -> })
        outbox(on = process, delays = { Backoff(60_000, 60_000) }).send(roomId, "slow", listOf(text("a.txt")))
        eventually { dao.filesOf(key(1)).single().timeouts == 1 }
        process.cancel()

        outbox()
        eventually { row(roomId).state == OutgoingEntity.FAILED }
        assertEquals(Outbox.MAX_UPLOAD_TIMEOUTS, uploads())
    }

    @Test
    fun retryOfAnUploadThatRanOutOfTimeStartsOver() = runBlocking {
        val roomId = loadedChannel(1)
        server.uploadSink = timingOut()
        val box = outbox()
        box.send(roomId, "slow", listOf(text("a.txt")))
        eventually { row(roomId).state == OutgoingEntity.FAILED }

        box.retry(key(1))
        eventually { uploads() == 2 * Outbox.MAX_UPLOAD_TIMEOUTS && row(roomId).state == OutgoingEntity.FAILED }
        assertEquals(2 * Outbox.MAX_UPLOAD_TIMEOUTS, uploads())

        server.uploadSink = { blackholeSink() }
        box.retry(key(1))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals("slow", server.sent(roomId).single().text)
    }

    @Test
    fun aConnectionBrokenWithoutATimeoutIsNotCounted() = runBlocking {
        val roomId = loadedChannel(1)
        val breaks = AtomicInteger(Outbox.MAX_UPLOAD_TIMEOUTS + 2)
        server.uploadSink = {
            if (breaks.getAndDecrement() > 0) timingOut { IOException("connection reset") }() else blackholeSink()
        }
        outbox().send(roomId, "flaky", listOf(text("a.txt")))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(1, server.sent(roomId).size)
    }

    @Test
    fun aConnectionThatCannotBeOpenedIsNotCounted() = runBlocking {
        val roomId = loadedChannel(1)
        val outage = AtomicInteger(Outbox.MAX_UPLOAD_TIMEOUTS + 2)
        // No byte has left: an outage, during which text messages wait as well.
        server.beforeUpload = { if (outage.getAndDecrement() > 0) throw SocketTimeoutException("connect timed out") }
        outbox().send(roomId, "later", listOf(text("a.txt")))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(1, server.sent(roomId).size)
    }

    /** The flag on disk as the closing boundary arrives; [also] runs then, before it is let through. */
    private fun recordingTheFlag(flag: AtomicReference<Boolean>, also: () -> Unit = {}): () -> Sink = {
        object : ForwardingSink(blackholeSink()) {
            override fun write(source: Buffer, byteCount: Long) {
                val stored = runBlocking { dao.filesOf(key(1)).single().maybeStored }
                if (source.indexOf("--\r\n".encodeUtf8()) >= 0) {
                    flag.set(stored)
                    also()
                } else {
                    assertFalse("set before everything else had left", stored)
                }
                super.write(source, byteCount)
            }
        }
    }

    @Test
    fun theFlagIsOnDiskWhenTheClosingBoundaryLeaves() = runBlocking {
        val roomId = loadedChannel(1)
        val flag = AtomicReference<Boolean>()
        server.uploadSink = recordingTheFlag(flag)
        outbox().send(roomId, "flagged", listOf(text("a.txt")))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals("the flag as the closing boundary arrived", true, flag.get())
    }

    @Test
    fun aProcessKilledAsTheClosingBoundaryLeavesFindsTheFlag() = runBlocking {
        val roomId = loadedChannel(1)
        val flag = AtomicReference<Boolean>()
        val leaving = CountDownLatch(1)
        val stuck = CountDownLatch(1)
        server.uploadSink = recordingTheFlag(flag) {
            leaving.countDown()
            stuck.await(10, TimeUnit.SECONDS)
        }
        val process = CoroutineScope(SupervisorJob() + Dispatchers.Default + CoroutineExceptionHandler { _, _ -> })
        try {
            outbox(on = process).send(roomId, "killed", listOf(text("a.txt")))
            assertTrue("the closing boundary did not leave", leaving.await(5, TimeUnit.SECONDS))
            process.cancel()
            server.uploadSink = { blackholeSink() }

            outbox()
            eventually { dao.storedOutgoing(roomId).singleOrNull()?.state != OutgoingEntity.QUEUED }
            assertEquals("uploads", 1, uploads())
            assertEquals(OutgoingEntity.FAILED, row(roomId).state)
            assertEquals(OutgoingEntity.ERROR_UPLOAD, row(roomId).error)
        } finally {
            stuck.countDown()
        }
    }

    @Test
    fun aStoredFileClearsTheFlag() = runBlocking {
        val roomId = loadedChannel(1)
        val flag = AtomicReference<Boolean>()
        server.uploadSink = recordingTheFlag(flag)
        // Keeps the row, so that its file can be read after the upload.
        server.intercept = { if (it == "send $roomId") 503 else null }
        outbox().send(roomId, "stored", listOf(text("a.txt")))
        eventually { dao.filesOf(key(1)).single().fileId != null }
        assertEquals("the flag as the closing boundary arrived", true, flag.get())
        assertFalse("the flag after a 201", dao.filesOf(key(1)).single().maybeStored)
    }

    @Test
    fun anAnswerThatTellsTheOutcomeClearsTheFlag() = runBlocking {
        val roomId = loadedChannel(1)
        val flag = AtomicReference<Boolean>()
        server.uploadSink = recordingTheFlag(flag)
        server.uploadStatus = { 503 }
        // No second attempt during this test: its start clears the flag too.
        outbox(delays = { Backoff(60_000, 60_000) }).send(roomId, "busy", listOf(text("a.txt")))
        eventually { flag.get() != null && !dao.filesOf(key(1)).single().maybeStored }
        assertEquals("the flag as the closing boundary arrived", true, flag.get())
        assertEquals("uploads", 1, uploads())
        assertEquals(OutgoingEntity.QUEUED, row(roomId).state)
    }

    @Test
    fun aFlagThatCannotBeWrittenKeepsTheMessageQueued() = runBlocking {
        val roomId = loadedChannel(1)
        // As a full disk fails the write, only for the flag being set.
        val db = database.openHelper.writableDatabase
        db.execSQL(
            "CREATE TRIGGER disk_full BEFORE UPDATE OF maybe_stored ON outgoing_files WHEN NEW.maybe_stored = 1 " +
                "BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END",
        )
        outbox().send(roomId, "stays", listOf(text("a.txt")))
        eventually { uploads() >= 2 }
        assertEquals(OutgoingEntity.QUEUED, row(roomId).state)
        assertEquals(0, server.uploads().size)

        db.execSQL("DROP TRIGGER disk_full")
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(1, server.uploads().size)
        assertEquals("stays", server.sent(roomId).single().text)
    }

    private suspend fun answeredWith(status: Int): OutgoingEntity? {
        val roomId = loadedChannel(status)
        server.intercept = { if (it == "upload $roomId") status else null }
        outbox().send(roomId, "answer $status", listOf(text("$status.txt")))
        // Failed, or repeated: null.
        var failed: OutgoingEntity? = null
        eventually {
            failed = dao.storedOutgoing(roomId).single().takeIf { it.state == OutgoingEntity.FAILED }
            failed != null || server.calls().count { it == "upload $roomId" } >= 2
        }
        return failed
    }

    @Test
    fun uploadAnswersAreClassified() = runBlocking {
        assertEquals(OutgoingEntity.ERROR_FILE, answeredWith(400)?.error)
        assertEquals(OutgoingEntity.ERROR_FILE, answeredWith(413)?.error)
        assertEquals(OutgoingEntity.ERROR_NO_ACCESS, answeredWith(404)?.error)
        assertEquals(OutgoingEntity.ERROR_UPLOAD, answeredWith(504)?.error)
        assertEquals(OutgoingEntity.ERROR_UPLOAD, answeredWith(500)?.error)
        // Not stored: repeated.
        assertEquals(null, answeredWith(503))
        assertEquals(null, answeredWith(401))
    }

    @Test
    fun aRateLimitedUploadWaitsAndRepeats() = runBlocking {
        val roomId = loadedChannel(1)
        val limited = AtomicInteger(1)
        server.intercept = { if (it == "upload $roomId" && limited.getAndDecrement() > 0) 429 else null }
        val start = System.currentTimeMillis()
        outbox().send(roomId, "limited", listOf(text("a.txt")))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertTrue(System.currentTimeMillis() - start >= 1_000)
        assertEquals(1, server.uploads().size)
    }

    @Test
    fun retryUploadsOnlyTheFilesWithoutAnId() = runBlocking {
        val roomId = loadedChannel(1)
        val answers = AtomicInteger()
        // The second file's answer is lost the first time.
        server.afterUpload = { if (answers.incrementAndGet() == 2) throw IOException("answer lost") }
        val box = outbox()
        box.send(roomId, "two", listOf(text("one.txt"), text("two.txt")))
        eventually { row(roomId).state == OutgoingEntity.FAILED }
        val first = dao.filesOf(row(roomId).clientMessageId).first().fileId

        box.retry(row(roomId).clientMessageId)
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(listOf("one.txt", "two.txt", "two.txt"), server.uploads().map { it.filename })
        val sent = server.sent(roomId).single().attachmentIds!!
        assertEquals(first, sent[0])
        assertEquals(server.uploads()[2].fileId, sent[1])
    }

    @Test
    fun retryAfterAConflictKeepsTheFilesUnderANewKey() = runBlocking {
        val roomId = loadedChannel(1)
        // Another text under the key the next message will get.
        server.store(roomId, me, key(1), "taken")
        val box = outbox()
        box.send(roomId, "mine", listOf(text("a.txt")))
        eventually { dao.storedOutgoing(roomId).singleOrNull()?.error == OutgoingEntity.ERROR_CONFLICT }
        val fileId = dao.filesOf(key(1)).single().fileId!!

        box.retry(key(1))
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(1, server.uploads().size)
        val sent = server.sent(roomId).last()
        assertEquals(key(2), sent.clientMessageId)
        assertEquals(listOf(fileId), sent.attachmentIds)
    }

    @Test
    fun anAnswerWithOtherFilesFailsTheMessage() = runBlocking {
        val roomId = loadedChannel(1)
        server.reply = { it.copy(attachmentIds = emptyList()) }
        outbox().send(roomId, "files", listOf(text("a.txt")))
        eventually { row(roomId).state == OutgoingEntity.FAILED }
        assertEquals(OutgoingEntity.ERROR_INVALID, row(roomId).error)
    }

    @Test
    fun cancelStopsAnUploadAndDeletesTheMessage() = runBlocking {
        val roomId = loadedChannel(1)
        val started = CompletableDeferred<Unit>()
        server.uploadSink = {
            object : ForwardingSink(blackholeSink()) {
                override fun write(source: Buffer, byteCount: Long) {
                    started.complete(Unit)
                    // A slow network.
                    Thread.sleep(50)
                    super.write(source, byteCount)
                }
            }
        }
        val box = outbox()
        box.send(roomId, "cancelled", listOf(stage("big.txt", ByteArray(2_000_000) { 'a'.code.toByte() })))
        box.send(roomId, "next")
        withTimeout(5_000) { started.await() }
        val key = dao.storedOutgoing(roomId).first().clientMessageId
        assertTrue(box.cancel(key))

        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(listOf("next"), server.sent(roomId).map { it.text })
        assertEquals(0, server.uploads().size)
        assertEquals(emptyList<String>(), stagedCopies())
    }

    @Test
    fun aMessageWhoseFilesAreAllUploadedCannotBeCancelled() = runBlocking {
        val roomId = loadedChannel(1)
        server.intercept = { if (it == "send $roomId") 503 else null }
        val box = outbox()
        box.send(roomId, "uploaded", listOf(text("a.txt")))
        eventually { dao.filesOf(row(roomId).clientMessageId).single().fileId != null }
        // It may already be stored.
        assertFalse(box.cancel(row(roomId).clientMessageId))
        assertEquals(1, dao.storedOutgoing(roomId).size)
    }

    @Test
    fun aMessageWithFilesHoldsTheNextOfItsRoomOnly() = runBlocking {
        val roomId = loadedChannel(1)
        val other = loadedChannel(2)
        val release = CompletableDeferred<Unit>()
        server.uploadSink = {
            object : ForwardingSink(blackholeSink()) {
                override fun write(source: Buffer, byteCount: Long) {
                    runBlocking { release.await() }
                    super.write(source, byteCount)
                }
            }
        }
        val box = outbox()
        box.send(roomId, "first", listOf(text("a.txt")))
        box.send(roomId, "second")
        box.send(other, "elsewhere")
        eventually { server.sent(other).isNotEmpty() }
        assertTrue(server.sent(roomId).isEmpty())
        release.complete(Unit)
        eventually { dao.storedOutgoing(roomId).isEmpty() }
        assertEquals(listOf("first", "second"), server.sent(roomId).map { it.text })
    }
}
