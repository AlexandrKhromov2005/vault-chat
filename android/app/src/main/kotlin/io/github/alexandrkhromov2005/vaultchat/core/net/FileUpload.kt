package io.github.alexandrkhromov2005.vaultchat.core.net

import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okio.BufferedSink
import okio.ByteString.Companion.encodeUtf8
import okio.source
import java.io.File
import java.util.UUID

/**
 * The multipart body of an upload (docs/android/attachments.md): one part
 * named `file` with [file]'s bytes under [name].
 *
 * The Gateway stores a file only once it has read the closing boundary, so
 * everything before it is flushed first and [beforeEnd] runs before its
 * first byte leaves: a failure before that cannot have stored the file.
 * [onStart] runs before the first byte of every attempt. The body is
 * one-shot, so OkHttp never sends it again by itself.
 */
class FileUpload(
    private val file: File,
    name: String,
    private val onStart: () -> Unit = {},
    private val beforeEnd: () -> Unit = {},
    private val onProgress: (sent: Long) -> Unit = {},
) : RequestBody() {
    private val boundary = "vaultchat-${UUID.randomUUID()}"
    private val head = (
        "--$boundary\r\n" +
            "Content-Disposition: form-data; name=\"file\"; filename=\"${quoted(name)}\"\r\n" +
            "Content-Type: application/octet-stream\r\n\r\n"
        ).encodeUtf8()
    private val tail = "\r\n--$boundary--\r\n".encodeUtf8()
    private val size = file.length()

    /** Whether the last attempt began to send the closing boundary. */
    @Volatile
    var endStarted = false
        private set

    override fun contentType(): MediaType = "multipart/form-data; boundary=$boundary".toMediaType()

    override fun contentLength(): Long = head.size + size + tail.size

    override fun isOneShot(): Boolean = true

    override fun writeTo(sink: BufferedSink) {
        endStarted = false
        onStart()
        sink.write(head)
        file.source().use { source ->
            var sent = 0L
            while (true) {
                val read = source.read(sink.buffer, SEGMENT_BYTES)
                if (read < 0) break
                sent += read
                if (sent > size) throw java.io.IOException("the file grew while it was sent")
                sink.emitCompleteSegments()
                onProgress(sent)
            }
            if (sent != size) throw java.io.IOException("the file shrank while it was sent")
        }
        sink.flush()
        beforeEnd()
        endStarted = true
        sink.write(tail)
        sink.flush()
    }

    private companion object {
        const val SEGMENT_BYTES = 64L * 1024

        // A quoted-string as Go's mime package reads it; names have no
        // controls (FileNames.upload), so only quotes and backslashes remain.
        fun quoted(name: String) = name.replace("\\", "\\\\").replace("\"", "\\\"")
    }
}
