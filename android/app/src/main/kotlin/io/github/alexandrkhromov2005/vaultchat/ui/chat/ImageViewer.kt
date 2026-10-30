package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ContentState
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileKind
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileNames
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity

/** A picture on the whole screen, zoomed with two fingers, with Save and Open. */
@Composable
fun ImageViewer(file: FileEntity, actions: FileActions) {
    val source = LocalFileSource.current
    val content by remember(source, file.fileId) { source.content(file) }.collectAsState(source.ready(file) ?: ContentState.Loading(0, file.size))
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    val transform = rememberTransformableState { zoom, pan, _ ->
        scale = (scale * zoom).coerceIn(1f, MAX_ZOOM)
        offset = if (scale == 1f) Offset.Zero else offset + pan
    }
    Dialog(
        onDismissRequest = actions::closeViewer,
        properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
    ) {
        Box(Modifier.fillMaxSize().background(Color.Black)) {
            when (val state = content) {
                is ContentState.Ready -> DecodedImage(
                    state.file,
                    FileKind.of(file.contentType),
                    ContentScale.Fit,
                    Modifier
                        .fillMaxSize()
                        .transformable(transform)
                        .graphicsLayer(scaleX = scale, scaleY = scale, translationX = offset.x, translationY = offset.y),
                )
                is ContentState.Loading -> CircularProgressIndicator(Modifier.align(Alignment.Center), color = Color.White)
                is ContentState.Failed, ContentState.Unavailable -> Text(
                    stringResource(if (state is ContentState.Failed) R.string.file_download_failed else R.string.file_unavailable),
                    color = Color.White,
                    modifier = Modifier.align(Alignment.Center),
                )
            }
            Row(
                Modifier.fillMaxWidth().background(Color.Black.copy(alpha = 0.5f)).safeDrawingPadding().padding(horizontal = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                IconButton(onClick = actions::closeViewer) {
                    Icon(painterResource(R.drawable.ic_close), contentDescription = stringResource(R.string.viewer_close), tint = Color.White)
                }
                Text(
                    FileNames.display(file.name),
                    color = Color.White,
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                IconButton(onClick = { actions.save(file) }) {
                    Icon(painterResource(R.drawable.ic_download), contentDescription = stringResource(R.string.file_save), tint = Color.White)
                }
                IconButton(onClick = { actions.open(file) }) {
                    Icon(painterResource(R.drawable.ic_open_in_new), contentDescription = stringResource(R.string.file_open), tint = Color.White)
                }
            }
        }
    }
}

private const val MAX_ZOOM = 5f
