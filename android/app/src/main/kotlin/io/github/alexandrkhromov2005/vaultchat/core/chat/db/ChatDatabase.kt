package io.github.alexandrkhromov2005.vaultchat.core.chat.db

import android.content.Context
import androidx.room.Database
import androidx.room.Room
import androidx.room.RoomDatabase
import io.github.alexandrkhromov2005.vaultchat.core.chat.SessionFiles
import java.io.File
import java.security.MessageDigest

/**
 * The chat data of one user on one server. Each pair has its own file, so
 * that nothing of one account is ever shown to another; files of other
 * sessions are deleted by [ChatDatabases.deleteAllExcept].
 */
@Database(
    entities = [
        RoomEntity::class, TimelineEntity::class, MessageEntity::class, ProfileEntity::class, OutgoingEntity::class,
        MemberEntity::class, RosterEntity::class, FileEntity::class, OutgoingFileEntity::class,
        NotifiedEntity::class,
    ],
    // 2: times in microseconds instead of milliseconds.
    // 3: the outgoing queue.
    // 4: the newest sequence when an outgoing row was queued.
    // 5: rosters of rooms.
    // 6: metadata of files, files of queued messages.
    // 7: what notifications have shown.
    version = 7,
)
abstract class ChatDatabase : RoomDatabase() {
    abstract fun dao(): ChatDao
}

/** Database files of the app's sessions. */
class ChatDatabases(private val context: Context) {

    fun open(server: String, userId: String): ChatDatabase =
        Room.databaseBuilder(context, ChatDatabase::class.java, fileName(server, userId))
            // There is no released schema yet; from the first release on,
            // every version change needs a migration and a test.
            .fallbackToDestructiveMigration(dropAllTables = true)
            .build()

    /** The private directories of the session whose database is [databaseName]. */
    fun files(databaseName: String): SessionFiles {
        val name = databaseName.removeSuffix(SUFFIX)
        return SessionFiles(cache = File(cacheRoot, name), staging = File(stagingRoot, name))
    }

    /** Deletes the database files and directories of every session except those named in [keep]. */
    fun deleteAllExcept(keep: Set<String>) {
        context.databaseList()
            .filter { it.startsWith(PREFIX) && it.endsWith(SUFFIX) && it !in keep }
            .forEach { context.deleteDatabase(it) }
        val kept = keep.map { it.removeSuffix(SUFFIX) }.toSet()
        for (root in listOf(cacheRoot, stagingRoot)) {
            root.listFiles().orEmpty().filter { it.name !in kept }.forEach { it.deleteRecursively() }
        }
    }

    // Under noBackupFilesDir, staged copies never enter a backup.
    private val cacheRoot get() = File(context.cacheDir, "attachments")
    private val stagingRoot get() = File(context.noBackupFilesDir, "outbox")

    companion object {
        private const val PREFIX = "chat-"
        private const val SUFFIX = ".db"

        /** The file name hides the server and user behind a hash. */
        fun fileName(server: String, userId: String): String {
            val digest = MessageDigest.getInstance("SHA-256").digest("$server\n$userId".toByteArray())
            return PREFIX + digest.take(16).joinToString("") { "%02x".format(it) } + SUFFIX
        }
    }
}
