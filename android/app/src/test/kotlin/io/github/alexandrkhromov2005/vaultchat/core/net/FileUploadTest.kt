package io.github.alexandrkhromov2005.vaultchat.core.net

import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Request
import okio.Buffer
import okio.ForwardingSink
import okio.blackholeSink
import okio.buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

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
