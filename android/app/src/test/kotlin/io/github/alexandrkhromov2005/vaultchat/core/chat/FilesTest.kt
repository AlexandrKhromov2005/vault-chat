package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.FileDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class FilesTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private val fileId = "00000000-0000-4000-8005-000000000001"
    private val roomId = "00000000-0000-4000-8001-000000000001"
    private val owner = "00000000-0000-4000-8000-0000000000aa"

    private fun dto(
        id: String = fileId,
        room: String = roomId,
        name: String = "notes.txt",
        type: String = "text/plain; charset=utf-8",
        size: Long = 5,
    ) = FileDto(id, room, owner, name, type, size, "2026-10-30T12:00:00.123456Z")

    @Test
    fun kindComesFromTheContentTypeWithoutItsParameters() {
        assertEquals(FileKind.TEXT, FileKind.of("text/plain; charset=utf-8"))
        assertEquals(FileKind.PNG, FileKind.of("IMAGE/PNG"))
        assertEquals(FileKind.JPEG, FileKind.of("image/jpeg"))
        assertEquals(FileKind.PDF, FileKind.of("application/pdf"))
        assertEquals(FileKind.OTHER, FileKind.of("image/svg+xml"))
        assertEquals(FileKind.OTHER, FileKind.of("text/html"))
        assertFalse(FileKind.OTHER.isImage)
    }

    @Test
    fun signaturesAreRecognised() {
        assertEquals(FileKind.PNG, FileKind.bySignature(byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0)))
        assertEquals(FileKind.JPEG, FileKind.bySignature(byteArrayOf(0xff.toByte(), 0xd8.toByte(), 0xff.toByte(), 0xe0.toByte())))
        assertEquals(FileKind.PDF, FileKind.bySignature("%PDF-1.7".toByteArray()))
        assertNull(FileKind.bySignature("<svg xmlns".toByteArray()))
        assertNull(FileKind.bySignature(byteArrayOf(0x89.toByte(), 0x50)))
    }

    @Test
    fun onlyAPictureWithItsSignatureIsDecodable() {
        val png = temporary.newFile().apply { writeBytes(byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2)) }
        val svg = temporary.newFile().apply { writeText("<svg xmlns=\"http://www.w3.org/2000/svg\"/>") }
        val short = temporary.newFile().apply { writeBytes(byteArrayOf(0xff.toByte())) }
        assertTrue(hasSignature(png, FileKind.PNG))
        // Metadata that claims PNG does not make other bytes a PNG.
        assertFalse(hasSignature(svg, FileKind.PNG))
        assertFalse(hasSignature(png, FileKind.JPEG))
        assertFalse(hasSignature(short, FileKind.JPEG))
        assertFalse(hasSignature(temporary.root.resolve("missing"), FileKind.PNG))
    }

    @Test
    fun otherAppsGetContentOnlyUnderTheTypeItHas() {
        val png = byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)
        val jpeg = byteArrayOf(0xff.toByte(), 0xd8.toByte(), 0xff.toByte(), 0xe0.toByte())
        val pdf = "%PDF-1.7".toByteArray()
        val text = "<svg on".toByteArray()

        assertEquals("image/png", viewableType(FileKind.PNG, png))
        assertEquals("image/jpeg", viewableType(FileKind.JPEG, jpeg))
        assertEquals("application/pdf", viewableType(FileKind.PDF, pdf))
        assertEquals("text/plain", viewableType(FileKind.TEXT, text))

        // Metadata that claims a type does not make other bytes of that type.
        for (head in listOf(jpeg, pdf, text, ByteArray(0))) assertNull(viewableType(FileKind.PNG, head))
        for (head in listOf(png, pdf, text, ByteArray(0))) assertNull(viewableType(FileKind.JPEG, head))
        for (head in listOf(png, jpeg, text, ByteArray(0))) assertNull(viewableType(FileKind.PDF, head))
        for (head in listOf(png, jpeg, pdf, byteArrayOf(0x41, 0, 0x42))) assertNull(viewableType(FileKind.TEXT, head))

        // Generic whatever it is: no app takes it for a known type.
        for (head in listOf(png, jpeg, pdf, text)) assertEquals("application/octet-stream", viewableType(FileKind.OTHER, head))
    }

    @Test
    fun shownNamesLoseFormatAndControlCharacters() {
        // U+202E reverses the visible order: "report‮gpj.exe" looks like "reportexe.jpg".
        assertEquals("reportgpj.exe", FileNames.display("report‮gpj.exe"))
        assertEquals("a b", FileNames.display("a b"))
        assertEquals("ab", FileNames.display("a\u0000\u001bb​"))
        assertEquals("file", FileNames.display(" ‮ "))
    }

    @Test
    fun localNamesAreSafeAndCarryTheExtensionOfTheType() {
        assertEquals("notes.txt", FileNames.local("notes.txt", FileKind.TEXT))
        assertEquals("photo.JPEG", FileNames.local("photo.JPEG", FileKind.JPEG))
        assertEquals("manual.js.txt", FileNames.local("manual.js", FileKind.TEXT))
        assertEquals("_.._etc_passwd.txt", FileNames.local("../../etc/passwd", FileKind.TEXT))
        assertEquals("file.pdf", FileNames.local("...", FileKind.PDF))
        assertEquals("a_b.bin", FileNames.local("a:b", FileKind.OTHER))
        val long = FileNames.local("x".repeat(300) + ".png", FileKind.PNG)
        assertTrue(long.toByteArray().size <= 255)
        assertTrue(long.endsWith(".png"))
    }

    @Test
    fun uploadNamesFitTheServersRules() {
        assertEquals("notes.txt", FileNames.upload("notes.txt"))
        assertEquals("a_b_c_d.txt", FileNames.upload("a/b\\c:d.txt"))
        assertEquals("name", FileNames.upload("  name.. "))
        assertEquals("file", FileNames.upload("..."))
        assertEquals("reportgpj.exe", FileNames.upload("report‮gpj.exe"))
        val long = FileNames.upload("я".repeat(200) + ".pdf")
        assertTrue(long.toByteArray().size <= MAX_FILE_NAME_BYTES)
        assertTrue(long.endsWith(".pdf"))
        val emoji = FileNames.upload("😀".repeat(100) + ".txt")
        assertTrue(emoji.toByteArray().size <= MAX_FILE_NAME_BYTES)
        assertFalse(Character.isLowSurrogate(emoji[0]))
    }

    @Test
    fun metadataIsCheckedBeforeItIsStored() {
        val stored = dto().toEntity(fileId, roomId)
        assertEquals("notes.txt", stored.name)
        assertEquals(1_793_361_600_123_456L, stored.createdAt)

        fun rejected(answer: FileDto) = assertThrows(ApiException::class.java) { answer.toEntity(fileId, roomId) }
        rejected(dto(id = "00000000-0000-4000-8005-000000000002"))
        rejected(dto(room = "00000000-0000-4000-8001-000000000002"))
        rejected(dto(size = 0))
        rejected(dto(size = MAX_FILE_BYTES + 1))
        rejected(dto(name = ""))
        rejected(dto(name = "x".repeat(256)))
        rejected(dto(type = ""))
        rejected(dto().copy(ownerId = "nobody"))
        rejected(dto().copy(createdAt = "yesterday"))
    }
}
