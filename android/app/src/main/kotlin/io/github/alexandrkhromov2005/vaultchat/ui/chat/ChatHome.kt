package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import io.github.alexandrkhromov2005.vaultchat.AppContainer
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import io.github.alexandrkhromov2005.vaultchat.ui.ScreenTransitions
import kotlinx.serialization.Serializable

@Serializable
private data object RoomsRoute

@Serializable
private data class ConversationRoute(val roomId: String)

@Serializable
private data object NewChatRoute

@Serializable
private data object CreateChannelRoute

@Serializable
private data class MembersRoute(val roomId: String)

@Serializable
private data class AddMemberRoute(val roomId: String)

/** The signed-in part of the app: the room list, conversations, new chats and members. */
@Composable
fun ChatHome(container: AppContainer, signedIn: SessionState.SignedIn) {
    val chat = remember(signedIn) { container.chats.get(signedIn) }
    val navController = rememberNavController()
    // A room just opened or created replaces the screens that led to it.
    val openRoom = { roomId: String ->
        navController.navigate(ConversationRoute(roomId)) {
            popUpTo<RoomsRoute>()
            launchSingleTop = true
        }
    }
    NavHost(
        navController,
        startDestination = RoomsRoute,
        enterTransition = ScreenTransitions.enter,
        exitTransition = ScreenTransitions.exit,
        popEnterTransition = ScreenTransitions.popEnter,
        popExitTransition = ScreenTransitions.popExit,
    ) {
        composable<RoomsRoute> {
            RoomsScreen(
                chat = chat,
                username = signedIn.username,
                server = signedIn.server.toString(),
                logout = container.session::logout,
                appScope = container.appScope,
                openRoom = { roomId -> navController.navigate(ConversationRoute(roomId)) { launchSingleTop = true } },
                newChat = { navController.navigate(NewChatRoute) { launchSingleTop = true } },
            )
        }
        composable<ConversationRoute> { entry ->
            val route = entry.toRoute<ConversationRoute>()
            ConversationScreen(
                chat,
                route.roomId,
                onBack = { navController.popOwn(entry) },
                onMembers = { navController.navigate(MembersRoute(route.roomId)) { launchSingleTop = true } },
            )
        }
        composable<NewChatRoute> { entry ->
            NewChatScreen(
                chat,
                onBack = { navController.popOwn(entry) },
                onOpened = openRoom,
                onCreateChannel = { navController.navigate(CreateChannelRoute) { launchSingleTop = true } },
            )
        }
        composable<CreateChannelRoute> { entry ->
            CreateChannelScreen(chat, onBack = { navController.popOwn(entry) }, onCreated = openRoom)
        }
        composable<MembersRoute> { entry ->
            val route = entry.toRoute<MembersRoute>()
            MembersScreen(
                chat,
                route.roomId,
                onBack = { navController.popOwn(entry) },
                // The room is gone, and with it the conversation below.
                onGone = { navController.popBackStack<RoomsRoute>(inclusive = false) },
                onAdd = { navController.navigate(AddMemberRoute(route.roomId)) { launchSingleTop = true } },
            )
        }
        composable<AddMemberRoute> { entry ->
            val route = entry.toRoute<AddMemberRoute>()
            AddMemberScreen(
                chat,
                route.roomId,
                onBack = { navController.popOwn(entry) },
                onGone = { navController.popBackStack<RoomsRoute>(inclusive = false) },
            )
        }
    }
}

/** Pops [entry] if it is on top, so that pressing back twice pops only it. */
private fun NavHostController.popOwn(entry: NavBackStackEntry) {
    if (currentBackStackEntry?.id == entry.id) popBackStack()
}
