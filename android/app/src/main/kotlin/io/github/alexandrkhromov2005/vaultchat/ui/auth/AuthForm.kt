package io.github.alexandrkhromov2005.vaultchat.ui.auth

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress

/** The scrollable, keyboard-aware column shared by the sign-in screens. */
@Composable
internal fun AuthLayout(content: @Composable ColumnScope.() -> Unit) {
    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Box(
            Modifier
                .fillMaxSize()
                .safeDrawingPadding()
                .imePadding()
                .verticalScroll(rememberScrollState()),
            contentAlignment = Alignment.TopCenter,
        ) {
            Column(
                Modifier
                    .widthIn(max = 420.dp)
                    .fillMaxWidth()
                    .padding(horizontal = 24.dp, vertical = 32.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
                content = content,
            )
        }
    }
}

@Composable
internal fun FormField(
    value: String,
    onValueChange: (String) -> Unit,
    @StringRes label: Int,
    keyboardType: KeyboardType,
    imeAction: ImeAction = ImeAction.Next,
    @StringRes hint: Int? = null,
    onDone: () -> Unit = {},
) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(stringResource(label)) },
        supportingText = hint?.let { { Text(stringResource(it)) } },
        singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = keyboardType, imeAction = imeAction, autoCorrectEnabled = false),
        keyboardActions = KeyboardActions(onDone = { onDone() }),
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
internal fun PasswordField(
    value: String,
    onValueChange: (String) -> Unit,
    imeAction: ImeAction,
    @StringRes hint: Int? = null,
    onDone: () -> Unit = {},
) {
    var visible by remember { mutableStateOf(false) }
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(stringResource(R.string.field_password)) },
        supportingText = hint?.let { { Text(stringResource(it)) } },
        singleLine = true,
        visualTransformation = if (visible) VisualTransformation.None else PasswordVisualTransformation(),
        trailingIcon = {
            TextButton(onClick = { visible = !visible }) {
                Text(stringResource(if (visible) R.string.password_hide else R.string.password_show))
            }
        },
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = imeAction, autoCorrectEnabled = false),
        keyboardActions = KeyboardActions(onDone = { onDone() }),
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
internal fun SubmitButton(@StringRes label: Int, busy: Boolean, enabled: Boolean, onClick: () -> Unit) {
    Button(
        onClick = onClick,
        enabled = enabled && !busy,
        modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
    ) {
        if (busy) {
            CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
        } else {
            Text(stringResource(label))
        }
    }
}

/** The failed attempt, announced by TalkBack when it appears. */
@Composable
internal fun ErrorMessage(error: AuthError?, registering: Boolean) {
    if (error == null) return
    Text(
        text = describe(error, registering),
        color = MaterialTheme.colorScheme.error,
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}

@Composable
private fun describe(error: AuthError, registering: Boolean): String = when (error) {
    is AuthError.Server -> stringResource(
        when (error.problem) {
            ServerAddress.Problem.EMPTY -> R.string.server_empty
            ServerAddress.Problem.MALFORMED -> R.string.server_malformed
            ServerAddress.Problem.CLEARTEXT -> R.string.server_cleartext
            ServerAddress.Problem.CREDENTIALS -> R.string.server_credentials
            ServerAddress.Problem.PATH -> R.string.server_path
        },
    )
    is AuthError.Api -> describe(error.error, registering)
    is AuthError.Registered -> stringResource(R.string.error_registered, describe(error.error, registering = false))
}

@Composable
private fun describe(error: ApiException, registering: Boolean): String = when (error.code) {
    ApiException.NETWORK -> stringResource(R.string.error_network)
    ApiException.INVALID_RESPONSE -> stringResource(R.string.error_invalid_response)
    ApiException.STORAGE -> stringResource(R.string.error_storage)
    "rate_limited" -> error.retryAfterSeconds
        ?.let { stringResource(R.string.error_rate_limited_for, it) }
        ?: stringResource(R.string.error_rate_limited)
    "unavailable", "timeout", "request_timeout" -> stringResource(R.string.error_unavailable)
    "unauthenticated" -> stringResource(R.string.error_credentials)
    "permission_denied" -> stringResource(if (registering) R.string.error_invitation else R.string.error_forbidden)
    "already_exists" -> stringResource(R.string.error_username_taken)
    "invalid_argument" -> when (error.message) {
        "invalid email address" -> stringResource(R.string.error_invalid_email)
        "invalid username" -> stringResource(R.string.error_invalid_username)
        "invalid password" -> stringResource(R.string.error_invalid_password)
        else -> stringResource(R.string.error_invalid_input)
    }
    else -> stringResource(R.string.error_unexpected, error.status)
}
