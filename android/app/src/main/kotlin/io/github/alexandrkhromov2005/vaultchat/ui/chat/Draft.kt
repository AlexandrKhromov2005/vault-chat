package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.MAX_ATTACHMENTS
import io.github.alexandrkhromov2005.vaultchat.core.chat.StagedFile
import io.github.alexandrkhromov2005.vaultchat.core.chat.Staging
import java.io.File
import java.io.InputStream

/** A picked file: its name and a way to read it once. */
class PickedFile(val name: String, val open: () -> InputStream?)

/**
 * Whether a picked URI may be read: only content of another app. Reading runs
 * with this app's own rights, so that a `file://` URI or one of its own
 * providers, returned by a picker that is not the system's, could otherwise
 * attach its private files to a message and send them.
 */
internal fun isPickable(uri: Uri, ownPackage: String): Boolean {
    if (uri.scheme != ContentResolver.SCHEME_CONTENT) return false
    // A prefix addresses the same provider for a user: content://0@authority.
    val authority = uri.authority?.substringAfterLast('@') ?: return false
    return authority != ownPackage && !authority.startsWith("$ownPackage.")
}

/** Reads picked content URIs with the grant the picker gave. */
fun ContentResolver.picked(uri: Uri): PickedFile {
    val name = try {
        query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
            if (cursor.moveToFirst()) cursor.getString(0) else null
        }
    } catch (e: RuntimeException) {
        null
    }
    return PickedFile(name ?: uri.lastPathSegment.orEmpty()) { openInputStream(uri) }
}

/** Why a picked file was not attached, for a message. */
data class DraftNotice(val name: String?, val problem: Staging.Problem?)

/** The clip button: Photo opens the system Photo Picker, File the document picker; neither needs a permission. */
@Composable
internal fun AttachButton(enabled: Boolean, onPicked: (List<Uri>) -> Unit) {
    var menu by remember { mutableStateOf(false) }
    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(MAX_ATTACHMENTS)) { onPicked(it) }
    val documents = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { onPicked(it) }
    Box {
        IconButton(onClick = { menu = true }, enabled = enabled) {
            Icon(painterResource(R.drawable.ic_attach_file), contentDescription = stringResource(R.string.composer_attach))
        }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            DropdownMenuItem(
                text = { Text(stringResource(R.string.composer_attach_photo)) },
                leadingIcon = { Icon(painterResource(R.drawable.ic_image), contentDescription = null) },
                onClick = {
                    menu = false
                    photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                },
            )
            DropdownMenuItem(
                text = { Text(stringResource(R.string.composer_attach_file)) },
                leadingIcon = { Icon(painterResource(R.drawable.ic_description), contentDescription = null) },
                onClick = {
                    menu = false
                    documents.launch(PICKABLE)
                },
            )
        }
    }
}

// What the server accepts (docs/media/files.md).
private val PICKABLE = arrayOf("image/png", "image/jpeg", "application/pdf", "text/plain")

/** The files picked for the next message, each with a button to remove it. */
@Composable
internal fun DraftFiles(files: List<StagedFile>, staging: Int, file: (String) -> File, onRemove: (String) -> Unit) {
    if (files.isEmpty() && staging == 0) return
    Row(
        Modifier.horizontalScroll(rememberScrollState()).padding(start = 8.dp, end = 8.dp, bottom = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        files.forEach { draft ->
            Box {
                if (draft.kind.isImage) {
                    DecodedImage(file(draft.staged), draft.kind, ContentScale.Crop, Modifier.size(64.dp).clip(RoundedCornerShape(10.dp)))
                } else {
                    Row(
                        Modifier
                            .clip(RoundedCornerShape(10.dp))
                            .background(MaterialTheme.colorScheme.surfaceContainerHighest)
                            .padding(start = 10.dp, end = 28.dp, top = 10.dp, bottom = 10.dp)
                            .widthIn(max = 180.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(painterResource(R.drawable.ic_description), contentDescription = null, modifier = Modifier.size(20.dp))
                        Text(
                            draft.name,
                            style = MaterialTheme.typography.bodySmall,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.padding(start = 6.dp),
                        )
                    }
                }
                Box(
                    Modifier
                        .align(Alignment.TopEnd)
                        .padding(2.dp)
                        .size(24.dp)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.inverseSurface.copy(alpha = 0.7f)),
                    contentAlignment = Alignment.Center,
                ) {
                    IconButton(onClick = { onRemove(draft.staged) }, modifier = Modifier.size(24.dp)) {
                        Icon(
                            painterResource(R.drawable.ic_close),
                            contentDescription = stringResource(R.string.composer_remove_file, draft.name),
                            tint = MaterialTheme.colorScheme.inverseOnSurface,
                            modifier = Modifier.size(16.dp),
                        )
                    }
                }
            }
        }
        if (staging > 0) CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
    }
}
