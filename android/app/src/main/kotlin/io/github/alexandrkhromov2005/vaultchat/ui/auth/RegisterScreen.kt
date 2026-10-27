package io.github.alexandrkhromov2005.vaultchat.ui.auth

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
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
import io.github.alexandrkhromov2005.vaultchat.R

@Composable
internal fun RegisterScreen(model: AuthViewModel, onBack: () -> Unit) {
    // The invitation and password are secrets and are not saved with the task.
    var invitation by remember { mutableStateOf("") }
    var email by rememberSaveable { mutableStateOf("") }
    var username by rememberSaveable { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    val canSubmit = invitation.isNotBlank() && email.isNotBlank() && username.isNotBlank() && password.isNotEmpty()
    val submit = { if (canSubmit) model.register(email, username, password, invitation) }

    BackHandler(enabled = !model.busy, onBack = onBack)

    AuthLayout {
        Text(
            stringResource(R.string.register_title),
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.SemiBold,
            modifier = Modifier.semantics { heading() },
        )
        Text(
            stringResource(R.string.register_subtitle),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        FormField(model.server, { model.server = it }, R.string.field_server, KeyboardType.Uri)
        FormField(invitation, { invitation = it }, R.string.field_invitation, KeyboardType.Ascii, hint = R.string.hint_invitation)
        FormField(email, { email = it }, R.string.field_email, KeyboardType.Email)
        FormField(username, { username = it }, R.string.field_username, KeyboardType.Ascii, hint = R.string.hint_username)
        PasswordField(password, { password = it }, ImeAction.Done, hint = R.string.hint_password, onDone = submit)
        ErrorMessage(model.error, registering = true)
        SubmitButton(R.string.register_submit, model.busy, canSubmit, submit)
        TextButton(onClick = onBack, enabled = !model.busy, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.register_to_login))
        }
    }
}
