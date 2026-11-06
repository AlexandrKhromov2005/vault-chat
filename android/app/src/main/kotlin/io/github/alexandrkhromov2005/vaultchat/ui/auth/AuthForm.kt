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
        text = describe(error, registering).text(),
        color = MaterialTheme.colorScheme.error,
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}

/** What the last attempt achieved, announced by TalkBack when it appears. */
@Composable
internal fun NoticeMessage(notice: AuthNotice?) {
    if (notice == null) return
    Text(
        text = stringResource(
            when (notice) {
                AuthNotice.CONFIRM_EMAIL -> R.string.notice_confirm_email
                AuthNotice.CONFIRMATION_REQUESTED -> R.string.notice_confirmation_requested
            },
        ),
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}

/** Whether login was refused until the account's email is confirmed. */
internal val AuthError?.awaitsConfirmation: Boolean
    get() = this is AuthError.Api && error.code == EMAIL_VERIFICATION_REQUIRED

private const val EMAIL_VERIFICATION_REQUIRED = "email_verification_required"

/** A string resource with its format arguments, which may be messages too. */
internal data class Message(@StringRes val id: Int, val args: List<Any> = emptyList())

@Composable
internal fun Message.text(): String = stringResource(id, *args.map { if (it is Message) it.text() else it }.toTypedArray())

internal fun describe(error: AuthError, registering: Boolean): Message = when (error) {
    is AuthError.Server -> Message(
        when (error.problem) {
            ServerAddress.Problem.EMPTY -> R.string.server_empty
            ServerAddress.Problem.MALFORMED -> R.string.server_malformed
            ServerAddress.Problem.CLEARTEXT -> R.string.server_cleartext
            ServerAddress.Problem.CREDENTIALS -> R.string.server_credentials
            ServerAddress.Problem.PATH -> R.string.server_path
        },
    )
    is AuthError.Api -> describe(error.error, registering)
    is AuthError.Registered -> Message(R.string.error_registered, listOf(describe(error.error, registering = false)))
}

private fun describe(error: ApiException, registering: Boolean): Message = when (error.code) {
    ApiException.NETWORK -> Message(R.string.error_network)
    ApiException.INVALID_RESPONSE -> Message(R.string.error_invalid_response)
    ApiException.STORAGE -> Message(R.string.error_storage)
    "rate_limited" -> error.retryAfterSeconds
        ?.let { Message(R.string.error_rate_limited_for, listOf(it)) }
        ?: Message(R.string.error_rate_limited)
    "unavailable", "timeout", "request_timeout" -> Message(R.string.error_unavailable)
    "unauthenticated" -> Message(R.string.error_credentials)
    EMAIL_VERIFICATION_REQUIRED -> Message(R.string.error_email_unconfirmed)
    "permission_denied" -> Message(if (registering) R.string.error_invitation else R.string.error_forbidden)
    "already_exists" -> Message(R.string.error_username_taken)
    "invalid_argument" -> when (error.message) {
        "invalid email address" -> Message(R.string.error_invalid_email)
        "invalid username" -> Message(R.string.error_invalid_username)
        "invalid password" -> Message(R.string.error_invalid_password)
        else -> Message(R.string.error_invalid_input)
    }
    else -> Message(R.string.error_unexpected, listOf(error.status))
}
