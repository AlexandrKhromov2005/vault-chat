package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import io.github.alexandrkhromov2005.vaultchat.AppContainer
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.serialization.Serializable

@Serializable
private data object RoomsRoute

@Serializable
private data class ConversationRoute(val roomId: String)

/** The signed-in part of the app: the room list and the open conversation. */
@Composable
fun ChatHome(container: AppContainer, signedIn: SessionState.SignedIn) {
    val chat = remember(signedIn) { container.chats.get(signedIn) }
    val navController = rememberNavController()
    NavHost(navController, startDestination = RoomsRoute) {
        composable<RoomsRoute> {
            RoomsScreen(
                chat = chat,
                username = signedIn.username,
                server = signedIn.server.toString(),
                logout = container.session::logout,
                appScope = container.appScope,
                openRoom = { roomId -> navController.navigate(ConversationRoute(roomId)) { launchSingleTop = true } },
            )
        }
        composable<ConversationRoute> { entry ->
            val route = entry.toRoute<ConversationRoute>()
            ConversationScreen(chat, route.roomId, onBack = {
                // Only this screen's own entry is popped, even if back is pressed twice.
                if (navController.currentBackStackEntry?.id == entry.id) navController.popBackStack()
            })
        }
    }
}
