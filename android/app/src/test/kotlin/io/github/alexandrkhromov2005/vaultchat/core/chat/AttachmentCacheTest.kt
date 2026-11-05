package io.github.alexandrkhromov2005.vaultchat.core.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.IOException

class AttachmentCacheTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private fun id(n: Int) = "00000000-0000-4000-8005-%012d".format(n)

    @Test
    fun aFileIsStoredWholeOrNotAtAll() {
        val root = temporary.newFolder()
        val cache = AttachmentCache(root)
        val stored = cache.put(id(1), "a.txt") { it.writeText("hello") }
        assertEquals("hello", stored.readText())
        assertNotNull(cache.get(id(1), "a.txt", 5))
        // Another size is not the stored file.
        assertNull(cache.get(id(1), "a.txt", 6))

        assertThrows(IOException::class.java) {
            cache.put(id(2), "b.txt") { part ->
                part.writeText("half")
                throw IOException("connection broke")
            }
        }
        assertNull(cache.get(id(2), "b.txt", 4))
        assertEquals(emptyList<String>(), root.resolve(id(2)).list()!!.toList())
    }

    @Test
    fun onlyAFileIdBecomesADirectory() {
        val cache = AttachmentCache(temporary.newFolder())
        assertThrows(IllegalArgumentException::class.java) { cache.get("../outbox", "a.txt", 1) }
        assertThrows(IllegalArgumentException::class.java) { cache.put("..", "a.txt") { it.writeText("x") } }
    }

    @Test
    fun theLeastRecentlyUsedGoFirstAndTheNewestStays() {
        val root = temporary.newFolder()
        val cache = AttachmentCache(root, limit = 10)
        cache.put(id(1), "a.bin") { it.writeBytes(ByteArray(4)) }
        cache.put(id(2), "b.bin") { it.writeBytes(ByteArray(4)) }
        root.resolve(id(1)).setLastModified(1_000)
        root.resolve(id(2)).setLastModified(2_000)
        // Reading touches it, so the other one is now the oldest.
        assertNotNull(cache.get(id(1), "a.bin", 4))
        cache.put(id(3), "c.bin") { it.writeBytes(ByteArray(4)) }
        cache.trim(keep = id(3))
        assertNotNull(cache.get(id(1), "a.bin", 4))
        assertNull(cache.get(id(2), "b.bin", 4))
        assertNotNull(cache.get(id(3), "c.bin", 4))

        // A file larger than the whole cache stays while it is the one just stored.
        cache.put(id(4), "d.bin") { it.writeBytes(ByteArray(20)) }
        cache.trim(keep = id(4))
        assertNotNull(cache.get(id(4), "d.bin", 20))
        assertEquals(listOf(id(4)), root.list()!!.toList())
    }

    @Test
    fun aLocalCopyIsMovedIn() {
        val cache = AttachmentCache(temporary.newFolder())
        val staged = temporary.newFile("staged").apply { writeText("picked") }
        val adopted = cache.adopt(id(1), "picked.txt", staged)
        assertEquals("picked", adopted!!.readText())
        assertTrue(!staged.exists())
    }
}
