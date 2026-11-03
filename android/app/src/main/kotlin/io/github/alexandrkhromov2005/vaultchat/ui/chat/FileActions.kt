package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.listSaver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.FileProvider
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ContentState
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileKind
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileNames
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileSource
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileState
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.viewableType
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException

/** The files of the conversation on screen; a source that shows nothing outside one. */
val LocalFileSource = staticCompositionLocalOf<FileSource> { NoFiles }

/** What the user can do with a file; null where nothing can be done, such as in previews. */
val LocalFileActions = staticCompositionLocalOf<FileActions?> { null }

private object NoFiles : FileSource {
    override fun metadata(roomId: String, fileId: String): Flow<FileState> = flowOf(FileState.Loading)

    override fun content(file: FileEntity): Flow<ContentState> = emptyFlow()

    override fun retry(fileId: String?) = Unit

    override fun staged(name: String): File = File(name)
}

/**
 * Opens, saves and views files (docs/android/attachments.md). A file is
 * downloaded first if it is not in the cache; [busy] holds those being
 * fetched, by how many actions wait for them, so that their rows show the
 * progress. Other apps get a `content://` URI of the cached copy with a
 * temporary read grant, never the access token.
 *
 * The system has created the document by the time a location is chosen, so
 * a save ends with the file in it, or with no document and a message.
 */
@Stable
class FileActions internal constructor(
    private val context: Context,
    private val source: FileSource,
    private val scope: CoroutineScope,
    private val notify: (String) -> Unit,
    /** The file to save while the system asks where; kept when the Activity is recreated meanwhile. */
    pendingSave: MutableState<FileEntity?> = mutableStateOf(null),
) {
    val busy = mutableStateMapOf<String, Int>()

    /** The image shown on the whole screen. */
    var viewing: FileEntity? by mutableStateOf(null)
        private set

    internal var createDocument: ActivityResultLauncher<DocumentRequest>? = null
    private var saving by pendingSave

    fun view(file: FileEntity) {
        viewing = file
    }

    fun closeViewer() {
        viewing = null
    }

    fun open(file: FileEntity) {
        // A second tap while it is being fetched.
        if (busy.containsKey(file.fileId)) return
        scope.launch(start = CoroutineStart.UNDISPATCHED) {
            val local = whileBusy(file) { fetched(file) } ?: return@launch
            val type = withContext(Dispatchers.IO) { viewableType(FileKind.of(file.contentType), local) }
            when {
                type == null -> notify(context.getString(R.string.file_type_mismatch))
                !startViewer(local, type) -> notify(context.getString(R.string.file_no_app))
            }
        }
    }

    /** Asks where to save [file], then copies it there. */
    fun save(file: FileEntity) {
        val launcher = createDocument ?: return
        saving = file
        val kind = FileKind.of(file.contentType)
        try {
            launcher.launch(DocumentRequest(FileNames.local(file.name, kind), kind.mime))
        } catch (e: ActivityNotFoundException) {
            saving = null
            notify(context.getString(R.string.file_save_failed))
        }
    }

    internal fun saveTo(uri: Uri?) {
        val file = saving
        saving = null
        if (uri == null) return
        // Started at once, so that being cancelled cannot skip the cleaning up.
        scope.launch(start = CoroutineStart.UNDISPATCHED) {
            var saved = false
            try {
                saved = file != null && whileBusy(file) { copyInto(file, uri) }
                if (file == null) notify(context.getString(R.string.file_save_failed))
            } catch (e: CancellationException) {
                // The screen is gone, and its messages with it.
                Toast.makeText(context.applicationContext, R.string.file_save_failed, Toast.LENGTH_LONG).show()
                throw e
            } finally {
                if (!saved) withContext(NonCancellable + Dispatchers.IO) { discard(uri) }
            }
        }
    }

    /** Copies [file] into the document at [uri] once it is here, and reports how that went. */
    private suspend fun copyInto(file: FileEntity, uri: Uri): Boolean {
        val local = fetched(file) ?: return false
        val saved = withContext(Dispatchers.IO) {
            try {
                val output = context.contentResolver.openOutputStream(uri, "wt") ?: return@withContext false
                output.use { out -> local.inputStream().use { it.copyTo(out) } }
                true
            } catch (e: IOException) {
                false
            } catch (e: RuntimeException) {
                // A provider's own failure, such as a SecurityException.
                false
            }
        }
        notify(context.getString(if (saved) R.string.file_saved else R.string.file_save_failed))
        return saved
    }

    /** Removes a document that did not get the file; one that cannot be deleted is at least emptied. */
    private fun discard(uri: Uri) {
        val resolver = context.contentResolver
        val deleted = try {
            DocumentsContract.deleteDocument(resolver, uri)
        } catch (e: IOException) {
            false
        } catch (e: RuntimeException) {
            false
        }
        if (deleted) return
        try {
            resolver.openOutputStream(uri, "wt")?.close()
        } catch (e: IOException) {
            // Nothing else to try; the user has been told that saving failed.
        } catch (e: RuntimeException) {
            // As above.
        }
    }

    private suspend fun <T> whileBusy(file: FileEntity, work: suspend () -> T): T {
        busy[file.fileId] = (busy[file.fileId] ?: 0) + 1
        try {
            return work()
        } finally {
            val left = busy.getValue(file.fileId) - 1
            if (left == 0) busy.remove(file.fileId) else busy[file.fileId] = left
        }
    }

    /** The cached copy of [file], downloaded if needed; null after telling the user why not. */
    private suspend fun fetched(file: FileEntity): File? =
        when (val state = source.content(file).first { it !is ContentState.Loading }) {
            is ContentState.Ready -> state.file
            ContentState.Unavailable -> null.also { notify(context.getString(R.string.file_unavailable)) }
            is ContentState.Failed -> null.also { notify(context.getString(R.string.file_download_failed)) }
            is ContentState.Loading -> null
        }

    private fun startViewer(local: File, type: String): Boolean {
        val uri = try {
            FileProvider.getUriForFile(context, "${context.packageName}.files", local)
        } catch (e: IllegalArgumentException) {
            return false
        }
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, type)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        return try {
            context.startActivity(intent)
            true
        } catch (e: ActivityNotFoundException) {
            false
        }
    }
}

/** A document to create with the system's picker: its suggested name and type. */
data class DocumentRequest(val name: String, val mime: String)

private object CreateDocument : ActivityResultContract<DocumentRequest, Uri?>() {
    override fun createIntent(context: Context, input: DocumentRequest): Intent =
        Intent(Intent.ACTION_CREATE_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType(input.mime)
            .putExtra(Intent.EXTRA_TITLE, input.name)

    override fun parseResult(resultCode: Int, intent: Intent?): Uri? = intent?.takeIf { resultCode == Activity.RESULT_OK }?.data
}

// A file's stored metadata, in the saved state while the system's picker is open.
private val PendingSaveSaver = listSaver<FileEntity?, String>(
    save = { file -> file?.run { listOf(fileId, roomId, ownerId, name, contentType, size.toString(), createdAt.toString()) }.orEmpty() },
    restore = { saved -> saved.takeIf { it.size == 7 }?.let { FileEntity(it[0], it[1], it[2], it[3], it[4], it[5].toLong(), it[6].toLong()) } },
)

@Composable
fun rememberFileActions(source: FileSource, notify: (String) -> Unit): FileActions {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val currentNotify by rememberUpdatedState(notify)
    val pendingSave = rememberSaveable(stateSaver = PendingSaveSaver) { mutableStateOf<FileEntity?>(null) }
    val actions = remember(source) { FileActions(context, source, scope, { currentNotify(it) }, pendingSave) }
    actions.createDocument = rememberLauncherForActivityResult(CreateDocument) { uri -> actions.saveTo(uri) }
    return actions
}
