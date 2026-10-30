package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.ComponentName
import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.paging.PagingData
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ContentState
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileSource
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileState
import io.github.alexandrkhromov2005.vaultchat.core.chat.UploadProgress
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageItem
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.OutgoingFileEntity
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.map
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.RuleChain
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.junit.runners.model.Statement
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import java.text.DecimalFormatSymbols
import java.util.Locale

/** Files in the bubbles of stored messages. */
@RunWith(RobolectricTestRunner::class)
class MessageFilesTest {
    private val compose = createComposeRule()
    private val temporary = TemporaryFolder()

    @get:Rule
    val rules: RuleChain = RuleChain.outerRule(temporary).around { base, _ ->
        object : Statement() {
            override fun evaluate() {
                val context = ApplicationProvider.getApplicationContext<Context>()
                shadowOf(context.packageManager).addActivityIfNotPresent(ComponentName(context, ComponentActivity::class.java))
                base.evaluate()
            }
        }
    }.around(compose)

    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val bob = "00000000-0000-4000-8000-0000000000bb"
    private val roomId = "00000000-0000-4000-8001-000000000001"
    private val now = 1_800_000_000_000_000L

    private fun id(n: Int) = "00000000-0000-4000-8005-%012d".format(n)

    private fun file(n: Int, name: String, type: String, size: Long = 2048) = FileEntity(id(n), roomId, bob, name, type, size, now)

    /** A source whose answers the test sets. */
    private class Source : FileSource {
        val metadata = MutableStateFlow<Map<String, FileState>>(emptyMap())
        val content = MutableStateFlow<Map<String, ContentState>>(emptyMap())
        val retried = mutableListOf<String?>()

        override fun metadata(roomId: String, fileId: String): Flow<FileState> = metadata.map { it[fileId] ?: FileState.Loading }

        override fun content(file: FileEntity): Flow<ContentState> = content.map { it[file.fileId] ?: ContentState.Loading(0, file.size) }

        override fun retry(fileId: String?) {
            retried += fileId
        }

        var stagingDir = java.io.File(".")

        override fun staged(name: String): java.io.File = java.io.File(stagingDir, name)
    }

    private val source = Source()

    private fun show(text: String, vararg fileIds: String) {
        val message = MessageEntity(roomId, 1, "m-1", bob, "cid-1", text, attachments = fileIds.joinToString(","), createdAt = now)
        val pages = MutableStateFlow(PagingData.from(listOf<ChatRow>(ChatRow.Message(MessageItem(message, "bob", true)))))
        compose.setContent {
            val actions = rememberFileActions(source) { }
            CompositionLocalProvider(LocalFileSource provides source, LocalFileActions provides actions) {
                Messages(pages.collectAsLazyPagingItems(), emptyList(), me = "me", channel = false, onNewestSeen = {}, onRetry = {}, onDelete = {})
            }
        }
        compose.waitForIdle()
    }

    private val me = "00000000-0000-4000-8000-0000000000aa"
    private val cid = "00000000-0000-4000-8004-000000000001"

    private fun outgoingRow() = OutgoingEntity(cid, roomId, me, position = 1, text = "", createdAt = now, state = OutgoingEntity.QUEUED)

    /** An own message of one file: queued with [files], then the [stored] history. */
    private fun showOutgoing(
        queue: MutableStateFlow<List<OutgoingEntity>>,
        files: MutableStateFlow<List<OutgoingFileEntity>>,
        uploads: Map<String, UploadProgress>,
        stored: MutableStateFlow<PagingData<ChatRow>>,
        onCancel: (String) -> Unit = {},
    ) {
        compose.setContent {
            val actions = rememberFileActions(source) { }
            val outgoing by remember { queue.handedOver { 0L } }.collectAsState(emptyList())
            // As the screen's model reads them.
            val shownFiles by remember { files.keptForHandoff() }.collectAsState(emptyMap())
            CompositionLocalProvider(LocalFileSource provides source, LocalFileActions provides actions) {
                Messages(
                    stored.collectAsLazyPagingItems(), outgoing, me, channel = false, onNewestSeen = {}, onRetry = {}, onDelete = {},
                    outgoingFiles = shownFiles, uploads = uploads, onCancel = onCancel,
                )
            }
        }
        compose.waitForIdle()
    }

    private fun shownName(name: String) = compose.onAllNodesWithText(name, useUnmergedTree = true).fetchSemanticsNodes().size

    @Test
    fun aMessageWhoseFileWaitsForItsUploadShowsTheCopyAndCanBeCancelled() {
        source.stagingDir = temporary.newFolder("staging")
        java.io.File(source.stagingDir, "s1").writeText("draft")
        val file = OutgoingFileEntity(cid, 0, "notes.txt", "text/plain", 5, "s1")
        val cancelled = mutableListOf<String>()
        showOutgoing(
            MutableStateFlow(listOf(outgoingRow())),
            MutableStateFlow(listOf(file)),
            mapOf(cid to UploadProgress(0, 2, 5)),
            MutableStateFlow(PagingData.from(emptyList())),
            onCancel = { cancelled += it },
        )
        assertEquals(1, shownName("notes.txt"))
        compose.onNode(hasContentDescription("1 вложение", substring = true)).performClick()
        compose.onNodeWithText(context.getString(R.string.message_cancel)).performClick()
        assertEquals(listOf(cid), cancelled)
    }

    @Test
    fun anUploadedFileStaysOnScreenWhileItsMessageIsHandedOver() {
        val uploaded = file(7, "report.pdf", "application/pdf")
        source.metadata.value = mapOf(uploaded.fileId to FileState.Known(uploaded))
        val queue = MutableStateFlow(listOf(outgoingRow()))
        val files = MutableStateFlow(listOf(OutgoingFileEntity(cid, 0, "report.pdf", "application/pdf", 2048, "s1", uploaded.fileId)))
        val stored = MutableStateFlow(PagingData.from(emptyList<ChatRow>()))
        showOutgoing(queue, files, emptyMap(), stored)
        assertEquals(1, shownName("report.pdf"))

        // The queue's observer sees the transaction first, and its files go with the row.
        queue.value = emptyList()
        files.value = emptyList()
        compose.waitForIdle()
        assertEquals("shown while the history reloads", 1, shownName("report.pdf"))

        val message = MessageEntity(roomId, 1, "m-1", me, cid, "", attachments = uploaded.fileId, createdAt = now)
        stored.value = PagingData.from(listOf(ChatRow.Message(MessageItem(message, null, true))))
        compose.waitForIdle()
        assertEquals(1, shownName("report.pdf"))
    }

    private fun bubble() = compose.onNode(hasContentDescription("pictures", substring = true)).fetchSemanticsNode()

    @Test
    fun aPictureArrivingDoesNotChangeTheBubblesSize() {
        val picture = file(1, "cat.png", "image/png")
        source.metadata.value = mapOf(picture.fileId to FileState.Known(picture))
        show("pictures", picture.fileId)
        val loading = bubble().size

        val png = temporary.newFile("cat.png").apply { writeBytes(byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)) }
        source.content.value = mapOf(picture.fileId to ContentState.Ready(png))
        compose.waitForIdle()
        assertEquals(loading, bubble().size)

        source.content.value = mapOf(picture.fileId to ContentState.Failed(io.github.alexandrkhromov2005.vaultchat.core.net.ApiException(0, "network", "x")))
        compose.waitForIdle()
        assertEquals(loading, bubble().size)
    }

    @Test
    fun onlyContentWithThePicturesSignatureIsDecoded() {
        val picture = file(1, "cat.png", "image/png")
        source.metadata.value = mapOf(picture.fileId to FileState.Known(picture))
        // The server says PNG; the bytes are SVG.
        val svg = temporary.newFile("cat.svg").apply { writeText("<svg xmlns=\"http://www.w3.org/2000/svg\"/>") }
        source.content.value = mapOf(picture.fileId to ContentState.Ready(svg))
        show("pictures", picture.fileId)
        compose.waitUntil(5_000) { compose.onAllNodesWithTag(BROKEN_TAG, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithTag(DECODED_TAG, useUnmergedTree = true).assertDoesNotExist()

        val png = temporary.newFile("cat.png").apply { writeBytes(byteArrayOf(0x89.toByte(), 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)) }
        source.content.value = mapOf(picture.fileId to ContentState.Ready(png))
        compose.waitUntil(5_000) { compose.onAllNodesWithTag(DECODED_TAG, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
    }

    @Test
    fun filesShowTheirNameAndSizeWithoutFormatCharacters() {
        val pdf = file(1, "report‮fdp.exe", "application/pdf", size = 3 * 1024 * 1024 / 2)
        source.metadata.value = mapOf(pdf.fileId to FileState.Known(pdf))
        show("a report", pdf.fileId)
        // The bubble speaks as one, so its parts are in the unmerged tree.
        compose.onNodeWithText("reportfdp.exe", useUnmergedTree = true).assertExists()
        val decimal = DecimalFormatSymbols.getInstance(Locale.getDefault()).decimalSeparator
        compose.onNodeWithText("1${decimal}5 МиБ", useUnmergedTree = true).assertExists()
    }

    @Test
    fun anUnavailableFileIsAskedForAgainWhenTapped() {
        source.metadata.value = mapOf(id(1) to FileState.Unavailable)
        show("gone", id(1))
        compose.onNodeWithText(context.getString(R.string.file_unavailable), useUnmergedTree = true).performClick()
        assertEquals(listOf<String?>(id(1)), source.retried)
    }

    @Test
    fun theBubbleOffersToOpenEachKnownFile() {
        val pdf = file(1, "report.pdf", "application/pdf")
        val picture = file(2, "cat.png", "image/png")
        source.metadata.value = mapOf(pdf.fileId to FileState.Known(pdf), picture.fileId to FileState.Known(picture), id(4) to FileState.Unavailable)
        show("pictures", pdf.fileId, picture.fileId, id(3), id(4))
        val actions = bubble().config[SemanticsActions.CustomActions]
        assertEquals(listOf("Открыть «report.pdf»", "Открыть «cat.png»", "Загрузить файл снова"), actions.map { it.label })
        actions.last().action()
        assertEquals(listOf<String?>(id(4)), source.retried)
        assertTrue(compose.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsActions.CustomActions)).fetchSemanticsNodes().size == 1)
    }
}
