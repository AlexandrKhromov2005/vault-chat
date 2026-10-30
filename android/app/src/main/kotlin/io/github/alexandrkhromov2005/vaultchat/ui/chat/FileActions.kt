package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
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
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
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
 * fetched, so that their rows show the progress. Other apps get a
 * `content://` URI of the cached copy with a temporary read grant, never
 * the access token.
 */
@Stable
class FileActions internal constructor(
    private val context: Context,
    private val source: FileSource,
    private val scope: CoroutineScope,
    private val notify: (String) -> Unit,
) {
    val busy = mutableStateMapOf<String, Unit>()

    /** The image shown on the whole screen. */
    var viewing: FileEntity? by mutableStateOf(null)
        private set

    internal var createDocument: ActivityResultLauncher<DocumentRequest>? = null
    private var saving: FileEntity? = null

    fun view(file: FileEntity) {
        viewing = file
    }

    fun closeViewer() {
        viewing = null
    }

    fun open(file: FileEntity) = fetch(file) { local ->
        if (!startViewer(local, FileKind.of(file.contentType))) notify(context.getString(R.string.file_no_app))
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
        val file = saving ?: return
        saving = null
        if (uri == null) return
        fetch(file) { local ->
            val saved = withContext(Dispatchers.IO) {
                try {
                    val output = context.contentResolver.openOutputStream(uri, "wt") ?: return@withContext false
                    output.use { out -> local.inputStream().use { it.copyTo(out) } }
                    true
                } catch (e: IOException) {
                    false
                } catch (e: SecurityException) {
                    false
                }
            }
            notify(context.getString(if (saved) R.string.file_saved else R.string.file_save_failed))
        }
    }

    private fun fetch(file: FileEntity, action: suspend (File) -> Unit) {
        if (busy.containsKey(file.fileId)) return
        busy[file.fileId] = Unit
        scope.launch {
            try {
                when (val state = source.content(file).first { it !is ContentState.Loading }) {
                    is ContentState.Ready -> action(state.file)
                    ContentState.Unavailable -> notify(context.getString(R.string.file_unavailable))
                    is ContentState.Failed -> notify(context.getString(R.string.file_download_failed))
                    is ContentState.Loading -> Unit
                }
            } finally {
                busy.remove(file.fileId)
            }
        }
    }

    private fun startViewer(local: File, kind: FileKind): Boolean {
        val uri = try {
            FileProvider.getUriForFile(context, "${context.packageName}.files", local)
        } catch (e: IllegalArgumentException) {
            return false
        }
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, kind.mime)
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

@Composable
fun rememberFileActions(source: FileSource, notify: (String) -> Unit): FileActions {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val currentNotify by rememberUpdatedState(notify)
    val actions = remember(source) { FileActions(context, source, scope) { currentNotify(it) } }
    actions.createDocument = rememberLauncherForActivityResult(CreateDocument) { uri -> actions.saveTo(uri) }
    return actions
}
