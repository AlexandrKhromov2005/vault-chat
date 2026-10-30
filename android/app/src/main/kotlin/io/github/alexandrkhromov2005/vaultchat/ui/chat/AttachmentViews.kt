package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.min
import coil3.compose.AsyncImage
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ContentState
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileKind
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileNames
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileState
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.hasSignature
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

/** A file of a message and what is known of it. */
internal data class MessageFile(val fileId: String, val state: FileState)

/** The metadata of a message's files as far as it is known, in the message's order. */
@Composable
internal fun rememberMessageFiles(roomId: String, fileIds: List<String>): List<MessageFile> {
    val source = LocalFileSource.current
    return fileIds.map { fileId ->
        key(fileId) {
            val state by remember(source, roomId, fileId) { source.metadata(roomId, fileId) }.collectAsState(FileState.Loading)
            MessageFile(fileId, state)
        }
    }
}

/**
 * Accessibility actions of a bubble whose own semantics are replaced: open
 * each known file, load again those that failed or were unavailable.
 */
@Composable
internal fun fileAccessibilityActions(files: List<MessageFile>): List<CustomAccessibilityAction> {
    val source = LocalFileSource.current
    val actions = LocalFileActions.current ?: return emptyList()
    val open = files.mapNotNull { (it.state as? FileState.Known)?.file }.map { file ->
        CustomAccessibilityAction(stringResource(R.string.file_open_named, FileNames.display(file.name))) {
            if (FileKind.of(file.contentType).isImage) actions.view(file) else actions.open(file)
            true
        }
    }
    val failed = files.filter { it.state is FileState.Failed || it.state == FileState.Unavailable }.map { it.fileId }
    if (failed.isEmpty()) return open
    return open + CustomAccessibilityAction(stringResource(R.string.file_retry)) {
        failed.forEach(source::retry)
        true
    }
}

/**
 * A message's files: pictures in a grid of fixed size, so that their content
 * arriving never changes the bubble's size, then other files as rows.
 */
@Composable
internal fun MessageFiles(files: List<MessageFile>, maxWidth: Dp) {
    if (files.isEmpty()) return
    val images = files.mapNotNull { (it.state as? FileState.Known)?.file?.takeIf { file -> FileKind.of(file.contentType).isImage } }
    val others = files.filterNot { it.state is FileState.Known && FileKind.of(it.state.file.contentType).isImage }
    // The bubble's padding is inside its width.
    val width = min(maxWidth - 24.dp, IMAGE_WIDTH)
    Column(Modifier.padding(vertical = 4.dp), verticalArrangement = Arrangement.spacedBy(GAP)) {
        if (images.size == 1) {
            ImageThumbnail(images.single(), Modifier.size(width, IMAGE_HEIGHT))
        } else {
            val side = (width - GAP) / 2
            images.chunked(2).forEach { pair ->
                Row(horizontalArrangement = Arrangement.spacedBy(GAP)) {
                    pair.forEach { key(it.fileId) { ImageThumbnail(it, Modifier.size(side)) } }
                }
            }
        }
        others.forEach { key(it.fileId) { FileRow(it, Modifier.width(width)) } }
    }
}

private val IMAGE_WIDTH = 260.dp
private val IMAGE_HEIGHT = 200.dp
private val GAP = 4.dp
private val CornerShape = RoundedCornerShape(12.dp)

@Composable
private fun ImageThumbnail(file: FileEntity, modifier: Modifier) {
    val source = LocalFileSource.current
    val actions = LocalFileActions.current
    val content by remember(source, file.fileId) { source.content(file) }.collectAsState(ContentState.Loading(0, file.size))
    val state = content
    Box(
        modifier
            .clip(CornerShape)
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .clickable {
                when (state) {
                    is ContentState.Ready -> actions?.view(file)
                    is ContentState.Failed, ContentState.Unavailable -> source.retry(file.fileId)
                    is ContentState.Loading -> Unit
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        when (state) {
            is ContentState.Ready -> DecodedImage(state.file, FileKind.of(file.contentType), ContentScale.Crop, Modifier.matchParentSize())
            is ContentState.Loading -> Progress(state)
            is ContentState.Failed -> FileIcon(R.drawable.ic_refresh)
            ContentState.Unavailable -> FileIcon(R.drawable.ic_broken_image)
        }
    }
}

/**
 * The picture in [file], decoded by the platform through Coil and scaled to
 * the size shown, but only if its content starts with the signature of
 * [kind] (PNG or JPEG); anything else shows a broken picture.
 */
@Composable
internal fun DecodedImage(file: File, kind: FileKind, scale: ContentScale, modifier: Modifier = Modifier) {
    val decodable by produceState<Boolean?>(null, file, kind) {
        value = kind.isImage && withContext(Dispatchers.IO) { hasSignature(file, kind) }
    }
    when (decodable) {
        true -> AsyncImage(model = file, contentDescription = null, contentScale = scale, modifier = modifier.testTag(DECODED_TAG))
        false -> Box(modifier.testTag(BROKEN_TAG), contentAlignment = Alignment.Center) { FileIcon(R.drawable.ic_broken_image) }
        null -> Unit
    }
}

internal const val DECODED_TAG = "decoded image"
internal const val BROKEN_TAG = "broken image"

@Composable
private fun Progress(state: ContentState.Loading) {
    if (state.received > 0 && state.total > 0) {
        CircularProgressIndicator(progress = { state.received.toFloat() / state.total }, modifier = Modifier.size(28.dp), strokeWidth = 3.dp)
    } else {
        CircularProgressIndicator(Modifier.size(28.dp), strokeWidth = 3.dp)
    }
}

// Decorative: the bubble's description names the files.
@Composable
private fun FileIcon(icon: Int) {
    Icon(painterResource(icon), contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
}

/** A file that is not a picture, or one whose metadata is not known yet. */
@Composable
private fun FileRow(row: MessageFile, modifier: Modifier) {
    val state = row.state
    val source = LocalFileSource.current
    val actions = LocalFileActions.current
    var menu by remember { mutableStateOf(false) }
    val file = (state as? FileState.Known)?.file
    val busy = file != null && actions?.busy?.containsKey(file.fileId) == true
    Box(modifier) {
        Row(
            Modifier
                .clip(CornerShape)
                .clickable {
                    when {
                        file != null -> menu = actions != null
                        state is FileState.Failed || state == FileState.Unavailable -> source.retry(row.fileId)
                        else -> Unit
                    }
                }
                .padding(vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Box(
                Modifier.size(40.dp).clip(CircleShape).background(MaterialTheme.colorScheme.surfaceVariant),
                contentAlignment = Alignment.Center,
            ) {
                when {
                    busy -> BusyProgress(file!!)
                    state is FileState.Loading -> CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                    state is FileState.Failed -> FileIcon(R.drawable.ic_refresh)
                    state == FileState.Unavailable -> FileIcon(R.drawable.ic_broken_image)
                    else -> FileIcon(R.drawable.ic_description)
                }
            }
            Column(Modifier.weight(1f, fill = false)) {
                val title = when (state) {
                    is FileState.Known -> FileNames.display(state.file.name)
                    FileState.Loading -> stringResource(R.string.file_loading)
                    FileState.Unavailable -> stringResource(R.string.file_unavailable)
                    is FileState.Failed -> stringResource(R.string.file_failed)
                }
                Text(title, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                if (file != null) {
                    Text(
                        formatSize(file.size),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        if (file != null && actions != null) {
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.file_open)) },
                    leadingIcon = { Icon(painterResource(R.drawable.ic_open_in_new), contentDescription = null) },
                    onClick = {
                        menu = false
                        actions.open(file)
                    },
                )
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.file_save)) },
                    leadingIcon = { Icon(painterResource(R.drawable.ic_download), contentDescription = null) },
                    onClick = {
                        menu = false
                        actions.save(file)
                    },
                )
            }
        }
    }
}

/** The progress of a file being fetched for an action; the download is shared with it. */
@Composable
private fun BusyProgress(file: FileEntity) {
    val source = LocalFileSource.current
    val content by remember(source, file.fileId) { source.content(file) }.collectAsState(ContentState.Loading(0, file.size))
    (content as? ContentState.Loading)?.let { Progress(it) }
}
