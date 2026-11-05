package io.github.alexandrkhromov2005.vaultchat.core.chat

import java.io.File
import java.io.IOException
import java.io.InputStream
import java.nio.ByteBuffer
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction
import java.util.UUID

/** A picked file copied into the session's staging directory, checked and ready to be queued. */
data class StagedFile(val staged: String, val name: String, val kind: FileKind, val size: Long)

/**
 * Copies picked files into the private [directory] (docs/android/attachments.md)
 * and checks them as the server will: 1 byte to 10 MiB, a PNG, JPEG or PDF
 * signature, or UTF-8 text without binary bytes. The server decides in the
 * end, also on what this does not check, such as text that is HTML.
 */
class Staging(private val directory: File) {
    /** Why a file cannot be attached. */
    enum class Problem { EMPTY, TOO_LARGE, UNSUPPORTED, UNREADABLE }

    sealed interface Result {
        data class Staged(val file: StagedFile) : Result

        data class Refused(val problem: Problem) : Result
    }

    /** Copies [input], closing it, under [name] made to fit the server's rules. */
    fun stage(name: String, input: InputStream): Result {
        if (!directory.isDirectory && !directory.mkdirs()) return Result.Refused(Problem.UNREADABLE)
        val staged = UUID.randomUUID().toString()
        val target = File(directory, staged)
        val outcome = try {
            input.use { copy(it, target) }
        } catch (e: IOException) {
            Result.Refused(Problem.UNREADABLE)
        }
        if (outcome is Result.Refused) {
            target.delete()
            return outcome
        }
        val size = target.length()
        val kind = kind(target) ?: run {
            target.delete()
            return Result.Refused(Problem.UNSUPPORTED)
        }
        return Result.Staged(StagedFile(staged, FileNames.upload(name), kind, size))
    }

    /** The staged copy named [staged]. */
    fun file(staged: String): File {
        require(isUuid(staged)) { "not a staged name" }
        return File(directory, staged)
    }

    /** Deletes the staged copies named in [staged]. */
    fun delete(staged: Collection<String>) {
        staged.filter(::isUuid).forEach { File(directory, it).delete() }
    }

    /**
     * Deletes copies that nothing in [referenced] names and that were
     * written before [before], such as a draft left by a killed process; a
     * draft being staged right now is newer.
     */
    fun sweep(referenced: Set<String>, before: Long) {
        directory.listFiles().orEmpty()
            .filter { it.name !in referenced && it.lastModified() < before }
            .forEach { it.delete() }
    }

    private fun copy(input: InputStream, target: File): Result? {
        var total = 0L
        target.outputStream().use { out ->
            val buffer = ByteArray(BUFFER_BYTES)
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                total += read
                if (total > MAX_FILE_BYTES) return Result.Refused(Problem.TOO_LARGE)
                out.write(buffer, 0, read)
            }
        }
        return if (total == 0L) Result.Refused(Problem.EMPTY) else null
    }

    private fun kind(file: File): FileKind? {
        val head = file.inputStream().use { input -> input.readUpTo(SNIFF_BYTES) }
        FileKind.bySignature(head)?.let { return it }
        // Go's http.DetectContentType calls these bytes binary.
        if (head.any { it.toInt() in BINARY }) return null
        return if (isUtf8(file)) FileKind.TEXT else null
    }

    private fun isUtf8(file: File): Boolean {
        val decoder = Charsets.UTF_8.newDecoder()
            .onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT)
        return try {
            decoder.decode(ByteBuffer.wrap(file.readBytes()))
            true
        } catch (e: CharacterCodingException) {
            false
        }
    }

    private fun InputStream.readUpTo(count: Int): ByteArray {
        val bytes = ByteArray(count)
        var read = 0
        while (read < count) {
            val n = read(bytes, read, count - read)
            if (n < 0) break
            read += n
        }
        return bytes.copyOf(read)
    }

    private companion object {
        const val BUFFER_BYTES = 64 * 1024

        // What http.DetectContentType looks at.
        const val SNIFF_BYTES = 512

        val BINARY = (0x00..0x08).toSet() + 0x0b + (0x0e..0x1a) + (0x1c..0x1f)
    }
}
