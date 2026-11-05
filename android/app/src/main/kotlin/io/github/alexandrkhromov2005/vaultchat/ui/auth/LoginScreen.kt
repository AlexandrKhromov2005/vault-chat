package io.github.alexandrkhromov2005.vaultchat.ui.auth

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import io.github.alexandrkhromov2005.vaultchat.R

@Composable
internal fun LoginScreen(model: AuthViewModel, sessionExpired: Boolean, onRegister: () -> Unit) {
    var email by rememberSaveable { mutableStateOf("") }
    // Not saveable: saved state can be written to disk with the task.
    var password by remember { mutableStateOf("") }
    val canSubmit = email.isNotBlank() && password.isNotEmpty()
    val submit = { if (canSubmit) model.login(email, password) }

    // An account was just created and has to be confirmed before login.
    LaunchedEffect(model.createdEmail) {
        model.createdEmail?.let {
            email = it
            model.createdEmail = null
        }
    }

    AuthLayout {
        Text(
            stringResource(R.string.app_name),
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.SemiBold,
            modifier = Modifier.semantics { heading() },
        )
        Text(
            stringResource(R.string.login_subtitle),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (sessionExpired) {
            Text(
                stringResource(R.string.session_expired),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(8.dp))
        FormField(model.server, { model.server = it }, R.string.field_server, KeyboardType.Uri)
        FormField(email, { email = it }, R.string.field_email, KeyboardType.Email)
        PasswordField(password, { password = it }, ImeAction.Done, onDone = submit)
        NoticeMessage(model.notice)
        ErrorMessage(model.error, registering = false)
        SubmitButton(R.string.login_submit, model.busy, canSubmit, submit)
        if (model.error.awaitsConfirmation || model.notice == AuthNotice.CONFIRM_EMAIL) {
            TextButton(
                onClick = { model.requestConfirmation(email, password) },
                enabled = canSubmit && !model.busy,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(stringResource(R.string.login_request_confirmation))
            }
        }
        TextButton(onClick = onRegister, enabled = !model.busy, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.login_to_register))
        }
    }
}
