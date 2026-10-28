package io.github.alexandrkhromov2005.vaultchat.ui.auth

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import io.github.alexandrkhromov2005.vaultchat.AppContainer
import io.github.alexandrkhromov2005.vaultchat.BuildConfig
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.serialization.Serializable

@Serializable
private object LoginRoute

@Serializable
private object RegisterRoute

/** Sign-in and registration, shown while nobody is signed in. */
@Composable
fun AuthFlow(container: AppContainer, reason: SessionState.Reason) {
    val model = viewModel {
        AuthViewModel(container.session, container.settings, container.allowLocalCleartext, BuildConfig.DEFAULT_SERVER)
    }
    val navController = rememberNavController()
    NavHost(navController, startDestination = LoginRoute) {
        composable<LoginRoute> {
            LoginScreen(
                model = model,
                sessionExpired = reason == SessionState.Reason.EXPIRED,
                onRegister = {
                    model.dismissError()
                    navController.navigate(RegisterRoute)
                },
            )
        }
        composable<RegisterRoute> {
            // The account awaits confirmation: the sign-in screen says so.
            LaunchedEffect(model.createdEmail) {
                if (model.createdEmail != null) navController.popBackStack()
            }
            RegisterScreen(
                model = model,
                onBack = {
                    model.dismissError()
                    navController.popBackStack()
                },
            )
        }
    }
}
