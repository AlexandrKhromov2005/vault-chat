package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallRole
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallView
import io.github.alexandrkhromov2005.vaultchat.core.calls.Calls

@Composable
fun rememberCallStarter(context: Context, calls: Calls, onDenied: () -> Unit): (String) -> Unit {
    var pendingRoom by remember { mutableStateOf<String?>(null) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) pendingRoom?.let(calls::start) else onDenied()
        pendingRoom = null
    }
    return remember(calls, context) {
        { roomId ->
            if (hasMicrophone(context)) calls.start(roomId) else {
                pendingRoom = roomId
                launcher.launch(Manifest.permission.RECORD_AUDIO)
            }
        }
    }
}

@Composable
fun CallPanel(calls: Calls) {
    val view by calls.view.collectAsStateWithLifecycle()
    view?.let { CallPanelView(it, calls) }
}

@Composable
private fun CallPanelView(view: CallView, calls: Calls) {
    val context = androidx.compose.ui.platform.LocalContext.current
    var denied by remember { mutableStateOf(false) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        denied = !granted
        if (granted) calls.accept()
    }
    Surface(tonalElevation = 3.dp, modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(statusText(view), style = MaterialTheme.typography.titleSmall)
            if (view.remote) Text(stringResource(R.string.call_remote), style = MaterialTheme.typography.bodySmall)
            if (denied) Text(stringResource(R.string.call_microphone_denied), color = MaterialTheme.colorScheme.error)
            view.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                when (view.state) {
                    "ringing" -> if (view.role == CallRole.CALLEE) {
                        TextButton(
                            enabled = !view.pending,
                            onClick = {
                                if (hasMicrophone(context)) calls.accept() else launcher.launch(Manifest.permission.RECORD_AUDIO)
                            },
                        ) { Text(stringResource(R.string.call_accept)) }
                        TextButton(enabled = !view.pending, onClick = calls::reject) { Text(stringResource(R.string.call_reject)) }
                    } else if (view.owned) {
                        TextButton(enabled = !view.pending, onClick = calls::cancel) { Text(stringResource(R.string.call_cancel)) }
                    }
                    "connecting", "active" -> if (view.owned) {
                        TextButton(onClick = calls::end) { Text(stringResource(R.string.call_end)) }
                    }
                    "ended" -> TextButton(onClick = calls::dismiss) { Text(stringResource(R.string.call_dismiss)) }
                }
            }
        }
    }
}

@Composable
private fun statusText(view: CallView): String = when {
    view.state == "starting" -> stringResource(R.string.call_starting)
    view.state == "ringing" && view.role == CallRole.CALLEE -> stringResource(R.string.call_incoming)
    view.state == "ringing" -> stringResource(R.string.call_outgoing)
    view.state == "connecting" -> stringResource(R.string.call_connecting)
    view.state == "active" -> stringResource(R.string.call_active_state)
    else -> stringResource(R.string.call_ended)
}

private fun hasMicrophone(context: Context): Boolean =
    androidx.core.content.ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED

