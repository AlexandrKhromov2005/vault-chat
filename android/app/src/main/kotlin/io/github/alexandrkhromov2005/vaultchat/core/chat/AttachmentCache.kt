package io.github.alexandrkhromov2005.vaultchat.core.chat

import java.io.File
import java.io.IOException
import java.util.UUID

/**
 * The private directories of one session's files (docs/android/attachments.md):
 * downloaded content in [cache], copies of picked files waiting to be sent in
 * [staging]. Both go with the session's database.
 */
data class SessionFiles(val cache: File, val staging: File) {
    /** Deletes both directories with everything in them. */
    fun wipe() {
        cache.deleteRecursively()
        staging.deleteRecursively()
    }
}

/**
 * Downloaded content of files, at most [limit] bytes: each file in a
 * directory named after its ID, under a name made from its metadata
 * (FileNames.local), which other apps see when it is opened. Directories
 * are touched when read and the least recently used go first.
 */
class AttachmentCache(private val root: File, private val limit: Long = LIMIT_BYTES) {

    /** The content of [fileId] if it is stored whole: [size] bytes under [name]. */
    fun get(fileId: String, name: String, size: Long): File? {
        val file = File(directory(fileId), name)
        if (!file.isFile || file.length() != size) return null
        file.parentFile?.setLastModified(System.currentTimeMillis())
        return file
    }

    /**
     * Writes the content of [fileId] through [write] into a temporary file,
     * whose name starts with a dot and so is never a FileNames.local name,
     * and stores it under [name] only once [write] returns. A failure leaves
     * nothing behind.
     */
    fun put(fileId: String, name: String, write: (File) -> Unit): File {
        val directory = directory(fileId)
        if (!directory.isDirectory && !directory.mkdirs()) throw IOException("cannot create the cache directory")
        // Unique, so that a download and an upload of one file cannot write into each other.
        val part = File(directory, ".part-${UUID.randomUUID()}")
        try {
            write(part)
            val file = File(directory, name)
            if (!part.renameTo(file)) throw IOException("cannot store the downloaded file")
            directory.setLastModified(System.currentTimeMillis())
            return file
        } finally {
            part.delete()
        }
    }

    /** Moves a local copy of [fileId] into the cache under [name], or returns null if it cannot. */
    fun adopt(fileId: String, name: String, source: File): File? = try {
        put(fileId, name) { part ->
            if (!source.renameTo(part)) {
                source.copyTo(part, overwrite = true)
                source.delete()
            }
        }
    } catch (e: IOException) {
        null
    }

    /** Deletes the least recently used files until the cache keeps to its limit; [keep] stays. */
    fun trim(keep: String? = null) {
        val directories = root.listFiles { file -> file.isDirectory }.orEmpty().sortedBy { it.lastModified() }
        var total = directories.sumOf { size(it) }
        for (directory in directories) {
            if (total <= limit) return
            if (directory.name == keep) continue
            total -= size(directory)
            directory.deleteRecursively()
        }
    }

    // File IDs come from the server; only a UUID becomes a directory name.
    private fun directory(fileId: String): File {
        require(isUuid(fileId)) { "not a file ID" }
        return File(root, fileId.lowercase())
    }

    private fun size(directory: File): Long = directory.listFiles().orEmpty().sumOf { it.length() }

    companion object {
        const val LIMIT_BYTES = 100L * 1024 * 1024
    }
}
