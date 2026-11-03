package io.github.alexandrkhromov2005.vaultchat.core.chat.db

import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.SQLiteMode
import java.io.File

/**
 * The app supports API 26, whose SQLite is 3.18. Robolectric's legacy mode runs
 * an engine older than that, so what fails here would also fail on those phones.
 */
@RunWith(RobolectricTestRunner::class)
@SQLiteMode(SQLiteMode.Mode.LEGACY)
class OldSqliteTest {
    private lateinit var database: ChatDatabase
    private lateinit var dao: ChatDao

    private fun room(id: String) = RoomEntity(id, RoomEntity.CHANNEL, "name", null, false, 0, null, null, 0)

    @Before
    fun setUp() {
        database = Room.inMemoryDatabaseBuilder(ApplicationProvider.getApplicationContext(), ChatDatabase::class.java)
            .allowMainThreadQueries()
            .build()
        dao = database.dao()
    }

    @After
    fun tearDown() {
        database.close()
    }

    private fun notified(roomId: String): Long? =
        database.openHelper.readableDatabase.query("SELECT sequence FROM notified WHERE room_id = ?", arrayOf(roomId)).use {
            if (it.moveToFirst()) it.getLong(0) else null
        }

    @Test
    fun theEngineIsAsOldAsThePhonesItStandsFor() {
        val version = database.openHelper.readableDatabase.query("SELECT sqlite_version()").use {
            it.moveToFirst()
            it.getString(0)
        }
        val (major, minor) = version.split('.').map(String::toInt)
        assertTrue("SQLite $version has the syntax of newer phones", major == 3 && minor < 24)
    }

    @Test
    fun marksOfNotificationsOnlyMoveForward() = runBlocking {
        dao.storeRoomList(listOf(room("room-1")), complete = true)

        dao.markNotified(mapOf("room-1" to 3L))
        assertEquals(3L, notified("room-1"))
        dao.markNotified(mapOf("room-1" to 5L))
        assertEquals(5L, notified("room-1"))
        dao.markNotified(mapOf("room-1" to 4L))
        assertEquals(5L, notified("room-1"))
    }

    @Test
    fun aRoomThatIsGoneGetsNoMark() = runBlocking {
        dao.markNotified(mapOf("room-gone" to 3L))
        assertEquals(null, notified("room-gone"))
    }

    @Test
    fun theRoomListUpsertsAndNoticesRunToo() = runBlocking {
        val me = "00000000-0000-4000-8000-0000000000aa"
        val bob = "00000000-0000-4000-8000-0000000000bb"
        dao.storeRoomList(listOf(room("room-1")), complete = true)
        // A second time over the stored row, as every sync does.
        dao.storeRoomList(listOf(room("room-1")), complete = true)
        dao.upsertProfiles(listOf(ProfileEntity(bob, "bob")))
        dao.upsertProfiles(listOf(ProfileEntity(bob, "bobby")))
        val message = MessageEntity("room-1", 1, "m-1", bob, "c-1", "hello", attachments = "", createdAt = 1)
        dao.applyLatest("room-1", listOf(message), readThrough = 0)

        assertEquals(1, dao.roomList(me).first().single().unread)
        assertEquals("bobby", dao.notices(me).single().senderName)
    }

    /**
     * Only a static check: the queries that the other tests do not run on the
     * old engine must not use syntax that API 26 does not have either.
     */
    @Test
    fun noQueryUsesSyntaxNewerThanTheOldestPhone() {
        val newer = listOf(
            "upsert (3.24)" to Regex("""ON\s+CONFLICT\s*(\([^)]*\))?\s*DO\b""", RegexOption.IGNORE_CASE),
            "RETURNING (3.35)" to Regex("""\bRETURNING\b""", RegexOption.IGNORE_CASE),
            "window function (3.25)" to Regex("""\bOVER\s*(\(|[A-Za-z_]+\s*"?\s*$)""", RegexOption.IGNORE_CASE),
            "aggregate filter (3.30)" to Regex("""\bFILTER\s*\(\s*WHERE\b""", RegexOption.IGNORE_CASE),
            "NULLS FIRST/LAST (3.30)" to Regex("""\bNULLS\s+(FIRST|LAST)\b""", RegexOption.IGNORE_CASE),
            "UPDATE FROM (3.33)" to Regex("""\bUPDATE\s+\w+\s+SET\b[^"]*\bFROM\b""", RegexOption.IGNORE_CASE),
            "iif (3.32)" to Regex("""\bIIF\s*\(""", RegexOption.IGNORE_CASE),
            "IS DISTINCT FROM (3.39)" to Regex("""\bIS\s+(NOT\s+)?DISTINCT\s+FROM\b""", RegexOption.IGNORE_CASE),
            "TRUE/FALSE literal (3.23)" to Regex("""=\s*(TRUE|FALSE)\b"""),
            "RENAME COLUMN (3.25)" to Regex("""\bRENAME\s+COLUMN\b""", RegexOption.IGNORE_CASE),
            "DROP COLUMN (3.35)" to Regex("""\bDROP\s+COLUMN\b""", RegexOption.IGNORE_CASE),
        )
        val sources = File("src/main/kotlin").walkTopDown().filter { it.extension == "kt" }.toList()
        assertTrue("no sources found from ${File(".").absolutePath}", sources.size > 10)
        val found = sources.flatMap { file ->
            // Queries are string literals; comments and identifiers are not asked about.
            val literals = Regex(""""(?:[^"\\\n]|\\.)*"""").findAll(file.readText()).map { it.value }.joinToString("\n")
            newer.filter { (_, pattern) -> pattern.containsMatchIn(literals) }.map { (name, _) -> "${file.name}: $name" }
        }
        assertEquals(emptyList<String>(), found)
    }
}
