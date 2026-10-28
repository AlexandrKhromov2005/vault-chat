package io.github.alexandrkhromov2005.vaultchat.core.chat.db

import android.content.Context
import androidx.room.Database
import androidx.room.Room
import androidx.room.RoomDatabase
import java.security.MessageDigest

/**
 * The chat data of one user on one server. Each pair has its own file, so
 * that nothing of one account is ever shown to another; files of other
 * sessions are deleted by [ChatDatabases.deleteAllExcept].
 */
@Database(
    entities = [RoomEntity::class, TimelineEntity::class, MessageEntity::class, ProfileEntity::class, OutgoingEntity::class],
    // 2: times in microseconds instead of milliseconds.
    // 3: the outgoing queue.
    version = 3,
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

    /** Deletes the database files of every session except those named in [keep]. */
    fun deleteAllExcept(keep: Set<String>) {
        context.databaseList()
            .filter { it.startsWith(PREFIX) && it.endsWith(SUFFIX) && it !in keep }
            .forEach { context.deleteDatabase(it) }
    }

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
