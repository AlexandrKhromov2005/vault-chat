package io.github.alexandrkhromov2005.vaultchat.ui.auth

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.alexandrkhromov2005.vaultchat.Settings
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionManager
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** What went wrong with a sign-in or registration attempt. */
sealed interface AuthError {
    data class Server(val problem: ServerAddress.Problem) : AuthError

    data class Api(val error: ApiException) : AuthError

    /** The account was created, but signing in to it failed. */
    data class Registered(val error: ApiException) : AuthError
}

/**
 * State shared by the sign-in and registration screens. A successful attempt
 * changes the session state, which replaces these screens.
 */
class AuthViewModel(
    private val session: SessionManager,
    private val settings: Settings,
    private val allowLocalCleartext: Boolean,
    defaultServer: String,
) : ViewModel() {

    var server by mutableStateOf(settings.lastServer ?: defaultServer)

    var busy by mutableStateOf(false)
        private set

    var error by mutableStateOf<AuthError?>(null)
        private set

    fun login(email: String, password: String) = submit { address ->
        session.login(address, email.trim(), password)
    }

    fun register(email: String, username: String, password: String, invitationToken: String) = submit { address ->
        session.register(address, email.trim(), username.trim(), password, invitationToken.trim())
        // A repeated registration would be rejected, so a failure from here on
        // is reported as such.
        try {
            session.login(address, email.trim(), password)
        } catch (e: ApiException) {
            throw RegisteredException(e)
        }
    }

    fun dismissError() {
        error = null
    }

    private fun submit(attempt: suspend (ServerAddress) -> Unit) {
        if (busy) return
        val address = try {
            ServerAddress.parse(server, allowLocalCleartext)
        } catch (e: ServerAddress.InvalidException) {
            error = AuthError.Server(e.problem)
            return
        }
        server = address.toString()
        settings.lastServer = server
        busy = true
        error = null
        viewModelScope.launch {
            try {
                attempt(address)
            } catch (e: RegisteredException) {
                error = AuthError.Registered(e.error)
            } catch (e: ApiException) {
                error = AuthError.Api(e)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // A defect must not crash the app on the sign-in screen.
                error = AuthError.Api(ApiException(0, "unexpected", e.javaClass.name, cause = e))
            } finally {
                busy = false
            }
        }
    }
}

private class RegisteredException(val error: ApiException) : Exception(error)
