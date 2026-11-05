package io.github.alexandrkhromov2005.vaultchat.core.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.IOException
import java.io.InputStream

class StagingTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private val png = byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0)
    private val jpeg = byteArrayOf(0xff.toByte(), 0xd8.toByte(), 0xff.toByte(), 0xe0.toByte())

    private fun staging() = Staging(temporary.root.resolve("staging"))

    private fun Staging.Result.staged() = (this as Staging.Result.Staged).file

    private fun Staging.Result.problem() = (this as Staging.Result.Refused).problem

    @Test
    fun theKindComesFromTheContent() {
        val staging = staging()
        assertEquals(FileKind.PNG, staging.stage("photo.jpg", png.inputStream()).staged().kind)
        assertEquals(FileKind.JPEG, staging.stage("photo", jpeg.inputStream()).staged().kind)
        assertEquals(FileKind.PDF, staging.stage("a.pdf", "%PDF-1.4\n".toByteArray().inputStream()).staged().kind)
        assertEquals(FileKind.TEXT, staging.stage("notes", "привет\tмир\n".toByteArray().inputStream()).staged().kind)
        // A UTF-8 byte order mark is still UTF-8 text.
        assertEquals(FileKind.TEXT, staging.stage("bom.txt", byteArrayOf(0xef.toByte(), 0xbb.toByte(), 0xbf.toByte(), 0x41).inputStream()).staged().kind)
    }

    @Test
    fun whatTheServerRefusesIsRefusedAtOnce() {
        val staging = staging()
        // A HEIC photo, a document of another type, UTF-16 text, broken UTF-8.
        val heic = byteArrayOf(0, 0, 0, 0x18, 0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x63)
        assertEquals(Staging.Problem.UNSUPPORTED, staging.stage("a.heic", heic.inputStream()).problem())
        assertEquals(Staging.Problem.UNSUPPORTED, staging.stage("a.docx", byteArrayOf(0x50, 0x4b, 3, 4, 20).inputStream()).problem())
        assertEquals(Staging.Problem.UNSUPPORTED, staging.stage("a.txt", byteArrayOf(0xff.toByte(), 0xfe.toByte(), 0x41, 0).inputStream()).problem())
        assertEquals(Staging.Problem.UNSUPPORTED, staging.stage("a.txt", byteArrayOf(0x41, 0xc3.toByte(), 0x28).inputStream()).problem())
        assertEquals(Staging.Problem.UNSUPPORTED, staging.stage("a.txt", "a\u0001b".toByteArray().inputStream()).problem())
        assertEquals(Staging.Problem.EMPTY, staging.stage("empty.txt", ByteArray(0).inputStream()).problem())
        // Nothing is left of what was refused.
        assertEquals(emptyList<String>(), temporary.root.resolve("staging").list()!!.toList())
    }

    @Test
    fun sizeIsLimitedWhileCopying() {
        val staging = staging()
        val limit = staging.stage("max.txt", Repeated('a'.code.toByte(), MAX_FILE_BYTES)).staged()
        assertEquals(MAX_FILE_BYTES, limit.size)
        // The copy stops at the first byte too many; an endless stream ends.
        assertEquals(Staging.Problem.TOO_LARGE, staging.stage("big.txt", Repeated('a'.code.toByte(), Long.MAX_VALUE)).problem())
        assertEquals(1, temporary.root.resolve("staging").list()!!.size)
    }

    @Test
    fun anUnreadableFileIsRefused() {
        val broken = object : InputStream() {
            override fun read(): Int = throw IOException("provider died")
            override fun read(b: ByteArray, off: Int, len: Int): Int = throw IOException("provider died")
        }
        assertEquals(Staging.Problem.UNREADABLE, staging().stage("a.txt", broken).problem())
    }

    @Test
    fun namesFitTheServersRules() {
        val staged = staging().stage("../re‮port:1.txt", "x".toByteArray().inputStream()).staged()
        assertEquals(".._report_1.txt", staged.name)
    }

    @Test
    fun onlyAStagedNameIsAFile() {
        val staging = staging()
        assertThrows(IllegalArgumentException::class.java) { staging.file("../session.bin") }
        val file = temporary.newFile("outside")
        staging.delete(listOf("../outside"))
        assertTrue(file.exists())
    }

    @Test
    fun unreferencedOldCopiesAreSwept() {
        val staging = staging()
        val queued = staging.stage("queued.txt", "a".toByteArray().inputStream()).staged()
        val orphan = staging.stage("orphan.txt", "b".toByteArray().inputStream()).staged()
        val draft = staging.stage("draft.txt", "c".toByteArray().inputStream()).staged()
        staging.file(queued.staged).setLastModified(1_000)
        staging.file(orphan.staged).setLastModified(1_000)
        staging.file(draft.staged).setLastModified(10_000)
        staging.sweep(referenced = setOf(queued.staged), before = 5_000)
        assertTrue(staging.file(queued.staged).exists())
        assertFalse(staging.file(orphan.staged).exists())
        // Staged after the session started: a draft of this process.
        assertTrue(staging.file(draft.staged).exists())
    }

    private class Repeated(private val byte: Byte, private var remaining: Long) : InputStream() {
        override fun read(): Int = if (remaining-- > 0) byte.toInt() else -1

        override fun read(b: ByteArray, off: Int, len: Int): Int {
            if (remaining <= 0) return -1
            val count = minOf(len.toLong(), remaining).toInt()
            b.fill(byte, off, off + count)
            remaining -= count
            return count
        }
    }
}
