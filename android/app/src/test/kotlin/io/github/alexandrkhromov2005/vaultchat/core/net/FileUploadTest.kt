package io.github.alexandrkhromov2005.vaultchat.core.net

import android.database.sqlite.SQLiteFullException
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okio.Buffer
import okio.ForwardingSink
import okio.blackholeSink
import okio.buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.IOException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

class FileUploadTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private val server = MockWebServer()

    @Before
    fun setUp() {
        server.start()
    }

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun theBodyIsOneMultipartPartNamedFile() {
        val file = temporary.newFile().apply { writeText("hello") }
        val body = FileUpload(file, "say \"hi\".txt")
        server.enqueue(MockResponse.Builder().code(201).build())
        OkHttpClient().newCall(Request.Builder().url(server.url("/files")).post(body).build()).execute().close()

        val request = server.takeRequest()
        val boundary = body.contentType().parameter("boundary")!!
        assertEquals("multipart/form-data; boundary=$boundary", request.headers["Content-Type"])
        assertEquals(
            "--$boundary\r\n" +
                "Content-Disposition: form-data; name=\"file\"; filename=\"say \\\"hi\\\".txt\"\r\n" +
                "Content-Type: application/octet-stream\r\n\r\n" +
                "hello\r\n--$boundary--\r\n",
            request.body!!.utf8(),
        )
        assertEquals(body.contentLength(), request.bodySize)
        assertTrue(body.isOneShot())
    }

    @Test
    fun everythingBeforeTheClosingBoundaryLeavesBeforeItIsMarked() {
        val file = temporary.newFile().apply { writeBytes(ByteArray(200_000) { it.toByte() }) }
        var left = 0L
        var leftAtMark = -1L
        val events = mutableListOf<String>()
        lateinit var body: FileUpload
        body = FileUpload(
            file,
            "a.bin",
            onStart = { events += "start" },
            beforeEnd = {
                leftAtMark = left
                assertFalse(body.endStarted)
                events += "end"
            },
        )
        val sink = object : ForwardingSink(blackholeSink()) {
            override fun write(source: Buffer, byteCount: Long) {
                left += byteCount
                super.write(source, byteCount)
            }
        }.buffer()
        body.writeTo(sink)
        sink.flush()
        val tail = "\r\n--${body.contentType().parameter("boundary")}--\r\n".length
        assertEquals(body.contentLength() - tail, leftAtMark)
        assertEquals(body.contentLength(), left)
        assertTrue(body.endStarted)
        assertEquals(listOf("start", "end"), events)
    }

    @Test
    fun aFailureBeforeTheClosingBoundaryLeavesTheMarkUnset() {
        val file = temporary.newFile().apply { writeBytes(ByteArray(200_000)) }
        var marked = false
        val body = FileUpload(file, "a.bin", beforeEnd = { marked = true })
        val sink = object : ForwardingSink(blackholeSink()) {
            private var left = 0L

            override fun write(source: Buffer, byteCount: Long) {
                left += byteCount
                if (left > 100_000) throw java.io.IOException("connection reset")
                super.write(source, byteCount)
            }
        }.buffer()
        runCatching { body.writeTo(sink) }
        assertFalse(marked)
        assertFalse(body.endStarted)
    }

    @Test
    fun progressCountsTheFilesBytes() {
        val file = temporary.newFile().apply { writeBytes(ByteArray(150_000)) }
        val seen = mutableListOf<Long>()
        FileUpload(file, "a.bin", onProgress = { seen += it }).writeTo(Buffer())
        assertEquals(150_000L, seen.last())
        assertEquals(seen.sorted(), seen)
    }
}

/** The marks of the upload body write to the database, on OkHttp's own thread. */
@RunWith(RobolectricTestRunner::class)
class FileUploadFailureTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private val server = MockWebServer()

    @Before
    fun setUp() {
        server.start()
    }

    @After
    fun tearDown() {
        server.close()
    }

    private fun assertFailsAsAnOrdinaryError(body: FileUpload) {
        val escaped = AtomicReference<Throwable>()
        val died = CountDownLatch(1)
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { _, e ->
            escaped.set(e)
            died.countDown()
        }
        try {
            server.enqueue(MockResponse.Builder().code(201).build())
            val client = OkHttpClient()
            val failure = AtomicReference<IOException>()
            val answered = CountDownLatch(1)
            client.newCall(Request.Builder().url(server.url("/files")).post(body).build()).enqueue(
                object : Callback {
                    override fun onFailure(call: Call, e: IOException) {
                        failure.set(e)
                        answered.countDown()
                    }

                    override fun onResponse(call: Call, response: Response) {
                        response.close()
                        answered.countDown()
                    }
                },
            )
            assertTrue("the call did not end", answered.await(5, TimeUnit.SECONDS))
            client.dispatcher.executorService.shutdown()
            assertTrue(client.dispatcher.executorService.awaitTermination(5, TimeUnit.SECONDS))
            died.await(500, TimeUnit.MILLISECONDS)
            // On a phone an uncaught exception on this thread ends the process.
            assertNull("it escaped OkHttp's thread", escaped.get())
            assertTrue(failure.get() != null)
            assertFalse(body.endStarted)
        } finally {
            Thread.setDefaultUncaughtExceptionHandler(previous)
        }
    }

    @Test
    fun aFailingMarkBeforeTheClosingBoundaryFailsTheCallNotTheProcess() {
        val file = temporary.newFile().apply { writeText("hello") }
        assertFailsAsAnOrdinaryError(FileUpload(file, "a.txt", beforeEnd = { throw SQLiteFullException("database or disk is full") }))
    }

    @Test
    fun aFailingMarkAtTheStartFailsTheCallNotTheProcess() {
        val file = temporary.newFile().apply { writeText("hello") }
        assertFailsAsAnOrdinaryError(FileUpload(file, "a.txt", onStart = { throw SQLiteFullException("database or disk is full") }))
    }
}
