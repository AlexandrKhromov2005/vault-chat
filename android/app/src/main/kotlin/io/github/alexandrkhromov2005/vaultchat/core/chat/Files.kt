package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.FileDto
import java.io.File
import java.io.IOException

// The Media service's rules for files (docs/media/files.md,
// internal/media/validator) and the checks of its answers.

/** The largest file the server accepts: 10 MiB. */
const val MAX_FILE_BYTES = 10L * 1024 * 1024

/** The longest file name the server accepts, in UTF-8 bytes. */
const val MAX_FILE_NAME_BYTES = 255

/** The types the server accepts, read from a file's content. */
enum class FileKind(val mime: String, val extension: String) {
    PNG("image/png", "png"),
    JPEG("image/jpeg", "jpg"),
    PDF("application/pdf", "pdf"),
    TEXT("text/plain", "txt"),

    /** A type the server does not store any more, or never did; never decoded or opened as a known type. */
    OTHER("application/octet-stream", "bin"),
    ;

    val isImage: Boolean get() = this == PNG || this == JPEG

    companion object {
        /** The kind of a stored file from its content type, parameters ignored. */
        fun of(contentType: String): FileKind = when (contentType.substringBefore(';').trim().lowercase()) {
            "image/png" -> PNG
            "image/jpeg" -> JPEG
            "application/pdf" -> PDF
            "text/plain" -> TEXT
            else -> OTHER
        }

        /**
         * The kind whose signature [head], the first bytes of a file, starts
         * with; text is not recognised here.
         */
        fun bySignature(head: ByteArray): FileKind? = when {
            head.startsWith(PNG_SIGNATURE) -> PNG
            head.startsWith(JPEG_SIGNATURE) -> JPEG
            head.startsWith(PDF_SIGNATURE) -> PDF
            else -> null
        }

        /** The bytes [bySignature] needs. */
        const val SIGNATURE_BYTES = 8

        private val PNG_SIGNATURE = byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)
        private val JPEG_SIGNATURE = byteArrayOf(0xff.toByte(), 0xd8.toByte(), 0xff.toByte())
        private val PDF_SIGNATURE = "%PDF-".toByteArray()

        private fun ByteArray.startsWith(prefix: ByteArray) =
            size >= prefix.size && prefix.indices.all { this[it] == prefix[it] }
    }
}

/**
 * Whether [file] starts with the signature of [kind]. Only a PNG or JPEG
 * whose content says so is decoded, whatever the metadata claims.
 */
fun hasSignature(file: File, kind: FileKind): Boolean = head(file)?.let { FileKind.bySignature(it) == kind } ?: false

/** Like the other [viewableType], for the content of [file]; null if it cannot be read. */
fun viewableType(kind: FileKind, file: File): String? = head(file)?.let { viewableType(kind, it) }

private fun head(file: File): ByteArray? {
    val head = ByteArray(FileKind.SIGNATURE_BYTES)
    var read = 0
    try {
        file.inputStream().use { input ->
            while (read < head.size) {
                val count = input.read(head, read, head.size - read)
                if (count < 0) break
                read += count
            }
        }
    } catch (e: IOException) {
        return null
    }
    return head.copyOf(read)
}

/**
 * The type under which content starting with [head] may be handed to another
 * app as a file of [kind]; null if the content is not what the metadata says.
 * An app picks its parser by this type, so that metadata that lies could
 * reach a parser the file was not checked for. Content of no known type goes
 * as a generic binary.
 */
fun viewableType(kind: FileKind, head: ByteArray): String? {
    val signature = FileKind.bySignature(head)
    val matches = when (kind) {
        FileKind.PNG, FileKind.JPEG, FileKind.PDF -> signature == kind
        FileKind.TEXT -> signature == null && head.none { it == 0.toByte() }
        FileKind.OTHER -> true
    }
    return kind.mime.takeIf { matches }
}

object FileNames {
    /**
     * A name to show: format characters, which could reverse the visible
     * order of an extension, and controls are removed. Never empty.
     */
    fun display(name: String): String {
        val shown = buildString {
            name.codePoints().forEach { codePoint ->
                when (Character.getType(codePoint).toByte()) {
                    Character.FORMAT, Character.CONTROL, Character.SURROGATE, Character.UNASSIGNED, Character.PRIVATE_USE -> Unit
                    Character.LINE_SEPARATOR, Character.PARAGRAPH_SEPARATOR -> append(' ')
                    else -> appendCodePoint(codePoint)
                }
            }
        }
        return trimSpace(shown).ifEmpty { FALLBACK }
    }

    /**
     * A name for a local copy, which other apps see: the shown name without
     * path separators or leading dots, with the extension of [kind], short
     * enough for any file system.
     */
    fun local(name: String, kind: FileKind): String {
        val base = display(name).map { if (it in "/\\:*?\"<>|") '_' else it }.joinToString("").trimStart('.')
        val extension = kind.extension
        val hasExtension = base.endsWith(".$extension", ignoreCase = true) ||
            (kind == FileKind.JPEG && base.endsWith(".jpeg", ignoreCase = true))
        val stem = (if (hasExtension) base.substringBeforeLast('.') else base).take(MAX_LOCAL_STEM).trimEnd('.', ' ')
        val suffix = if (hasExtension) base.substringAfterLast('.') else extension
        return "${stem.ifEmpty { FALLBACK }}.$suffix"
    }

    /**
     * A picked file's name made to fit the server's rules: controls and
     * format characters dropped, `/ \ :` replaced, no surrounding whitespace
     * or trailing dot, at most [MAX_FILE_NAME_BYTES]. The server adds the
     * extension of the type it detects if this one differs.
     */
    fun upload(name: String): String {
        val cleaned = display(name).map { if (it in "/\\:") '_' else it }.joinToString("")
        var result = trimSpace(cleaned).trimEnd('.')
        while (result.toByteArray().size > MAX_FILE_NAME_BYTES) {
            // Keeps the end, which holds the extension; never splits a surrogate pair.
            result = result.substring(Character.charCount(result.codePointAt(0)))
        }
        result = trimSpace(result).trimEnd('.')
        return result.ifEmpty { FALLBACK }
    }

    private const val FALLBACK = "file"

    // Leaves room for a dot and an extension within 255 bytes in UTF-8.
    private const val MAX_LOCAL_STEM = 60
}

/**
 * The metadata of [fileId] as stored, after checking that the Gateway
 * answered about that file of [roomId] with values the API allows.
 */
internal fun FileDto.toEntity(fileId: String, roomId: String): FileEntity {
    if (this.fileId != fileId) throw invalid("metadata of another file")
    if (this.roomId != roomId) throw invalid("file of another room")
    if (!isUuid(ownerId)) throw invalid("owner_id is not a UUID")
    if (size < 1 || size > MAX_FILE_BYTES) throw invalid("file size out of range")
    if (filename.isEmpty() || filename.toByteArray().size > MAX_FILE_NAME_BYTES) throw invalid("file name out of range")
    if (contentType.isEmpty() || contentType.length > MAX_CONTENT_TYPE_LENGTH) throw invalid("content type out of range")
    return FileEntity(
        fileId = fileId,
        roomId = roomId,
        ownerId = ownerId,
        name = filename,
        contentType = contentType,
        size = size,
        createdAt = parseMicros(createdAt) ?: throw invalid("created_at is not a time"),
    )
}

/**
 * The metadata of a file this user has just uploaded to [roomId], checked
 * against what was sent: this user's file of [size] bytes in that room.
 */
internal fun FileDto.toUploaded(roomId: String, me: String, size: Long): FileEntity {
    if (!isUuid(fileId)) throw invalid("file_id is not a UUID")
    if (ownerId != me) throw invalid("file of another user")
    if (this.size != size) throw invalid("not the uploaded size")
    return toEntity(fileId, roomId)
}

private const val MAX_CONTENT_TYPE_LENGTH = 255
