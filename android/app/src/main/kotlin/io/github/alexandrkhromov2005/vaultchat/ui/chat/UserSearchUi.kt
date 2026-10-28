package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.UserQuery
import io.github.alexandrkhromov2005.vaultchat.core.chat.UserSearch
import io.github.alexandrkhromov2005.vaultchat.core.net.PublicProfile
import io.github.alexandrkhromov2005.vaultchat.ui.auth.text

/** The search field in a screen's top bar; it takes the focus when the screen opens. */
@Composable
internal fun SearchField(text: String, onText: (String) -> Unit) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    TextField(
        value = text,
        onValueChange = onText,
        placeholder = { Text(stringResource(R.string.search_users_hint)) },
        singleLine = true,
        trailingIcon = {
            if (text.isNotEmpty()) {
                IconButton(onClick = { onText("") }) {
                    Icon(painterResource(R.drawable.ic_close), contentDescription = stringResource(R.string.search_clear))
                }
            }
        },
        colors = TextFieldDefaults.colors(
            focusedContainerColor = Color.Transparent,
            unfocusedContainerColor = Color.Transparent,
            focusedIndicatorColor = Color.Transparent,
            unfocusedIndicatorColor = Color.Transparent,
        ),
        keyboardOptions = KeyboardOptions(
            capitalization = KeyboardCapitalization.None,
            autoCorrectEnabled = false,
            keyboardType = KeyboardType.Ascii,
            imeAction = ImeAction.Search,
        ),
        modifier = Modifier.fillMaxWidth().focusRequester(focus),
    )
}

/**
 * The results of a user search. [busy] is the user whose choice is being
 * carried out; while one is, no other can be chosen. [taken] users are
 * shown with [takenLabel] and cannot be chosen.
 */
internal fun LazyListScope.userResults(
    state: UserSearch.State,
    busy: String?,
    onPick: (PublicProfile) -> Unit,
    onRetry: () -> Unit,
    taken: Set<String> = emptySet(),
    takenLabel: String? = null,
) {
    if (state.searching) item(key = "progress") { LinearProgressIndicator(Modifier.fillMaxWidth()) }
    val note = when {
        state.problem == UserQuery.Problem.TOO_SHORT -> R.string.search_users_start
        state.problem == UserQuery.Problem.INVALID -> R.string.search_users_invalid
        state.error == null && !state.searching && state.users.isEmpty() -> R.string.search_users_nothing
        else -> null
    }
    if (note != null) item(key = "note") { Note(stringResource(note)) }
    state.error?.let { error ->
        item(key = "error") {
            Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp)) {
                Text(
                    describe(error, ChatAction.SEARCH).text(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                )
                TextButton(onClick = onRetry) { Text(stringResource(R.string.retry)) }
            }
        }
    }
    if (state.problem == null) {
        items(state.users, key = { "u" + it.userId }) { user ->
            val isTaken = user.userId in taken
            UserRow(
                name = user.username,
                label = if (isTaken) takenLabel else null,
                busy = busy == user.userId,
                onClick = if (isTaken || busy != null) null else ({ onPick(user) }),
            )
        }
    }
}

@Composable
internal fun Note(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 16.dp),
    )
}

/** A person in a list: avatar, name, an optional label and an optional action at the end. */
@Composable
internal fun UserRow(
    name: String,
    label: String? = null,
    busy: Boolean = false,
    onClick: (() -> Unit)? = null,
    trailing: @Composable (() -> Unit)? = null,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .heightIn(min = 64.dp)
            .padding(start = 16.dp, end = 8.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Avatar(name, size = 40)
        Column(Modifier.weight(1f)) {
            Text(name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (label != null) {
                Text(label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        if (busy) CircularProgressIndicator(Modifier.padding(end = 8.dp).size(20.dp), strokeWidth = 2.dp)
        trailing?.invoke()
    }
}

/** A row that starts an action, such as creating a channel, above a list. */
@Composable
internal fun ActionRow(icon: Int, text: String, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).heightIn(min = 56.dp).padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(22.dp),
    ) {
        Icon(
            painterResource(icon),
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(start = 8.dp),
        )
        Text(text, style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.primary)
    }
}
