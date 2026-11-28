package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.Context
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
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
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallConnection
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallRole
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallView
import io.github.alexandrkhromov2005.vaultchat.core.calls.Calls

@Composable
fun rememberCallStarter(context: Context, calls: Calls, onDenied: () -> Unit): (String) -> Unit {
    var pendingRoom by remember { mutableStateOf<String?>(null) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { results ->
        if (canProceedWithCall(results)) pendingRoom?.let(calls::start) else onDenied()
        pendingRoom = null
    }
    return remember(calls, context) {
        { roomId ->
            val missing = missingCallPermissions(context)
            if (missing.isEmpty()) calls.start(roomId) else {
                pendingRoom = roomId
                launcher.launch(missing.toTypedArray())
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
internal fun CallStatus(view: CallView) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(statusText(view), style = MaterialTheme.typography.titleSmall)
        CallProgress(view)
        Text(detailText(view), style = MaterialTheme.typography.bodySmall)
        connectionText(view)?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.primary) }
    }
}

@Composable
private fun CallProgress(view: CallView) {
    val stage = when (view.state) {
        "starting" -> 1
        "ringing" -> 2
        "connecting" -> 3
        else -> 4
    }
    Text(stringResource(R.string.call_progress, stage), style = MaterialTheme.typography.labelSmall)
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        val labels = listOf(R.string.call_stage_prepare, R.string.call_stage_ring, R.string.call_stage_accept, R.string.call_stage_media)
        labels.forEachIndexed { index, label ->
            Column(Modifier.weight(1f)) {
                Box(
                    Modifier.fillMaxWidth().height(3.dp).background(
                        if (index < stage) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceContainerHighest,
                    ),
                )
                Text(
                    stringResource(label),
                    style = MaterialTheme.typography.labelSmall,
                    color = if (index < stage) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun detailText(view: CallView): String = when {
    view.state == "starting" -> stringResource(R.string.call_detail_starting)
    view.state == "ringing" && view.role == CallRole.CALLEE -> stringResource(R.string.call_detail_ringing_callee)
    view.state == "ringing" -> stringResource(R.string.call_detail_ringing_caller)
    view.state == "connecting" -> stringResource(R.string.call_detail_connecting)
    view.state == "active" && view.remote -> stringResource(R.string.call_detail_active)
    view.state == "active" -> stringResource(R.string.call_detail_active_wait)
    else -> stringResource(R.string.call_detail_ended)
}

@Composable
private fun connectionText(view: CallView): String? = when {
    view.connection == CallConnection.RECOVERING -> stringResource(R.string.call_connection_recovering)
    view.state == "active" && view.connection == CallConnection.DIRECT -> stringResource(R.string.call_connection_direct)
    view.state == "active" && view.connection == CallConnection.RELAY -> stringResource(R.string.call_connection_relay)
    view.state in setOf("connecting", "active") -> stringResource(R.string.call_connection_unknown)
    else -> null
}

@Composable
private fun CallPanelView(view: CallView, calls: Calls) {
    val context = androidx.compose.ui.platform.LocalContext.current
    var denied by remember { mutableStateOf(false) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { results ->
        denied = !canProceedWithCall(results)
        if (!denied) calls.accept()
    }
    Surface(tonalElevation = 3.dp, modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            CallStatus(view)
            if (denied) Text(stringResource(R.string.call_microphone_denied), color = MaterialTheme.colorScheme.error)
            view.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                when (view.state) {
                    "ringing" -> if (view.role == CallRole.CALLEE) {
                        TextButton(
                            enabled = !view.pending,
                            onClick = {
                                val missing = missingCallPermissions(context)
                                if (missing.isEmpty()) calls.accept() else launcher.launch(missing.toTypedArray())
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


