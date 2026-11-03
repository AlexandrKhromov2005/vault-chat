package io.github.alexandrkhromov2005.vaultchat.ui

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.AnimatedContentTransitionScope.SlideDirection
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.IntOffset
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost

/**
 * Screens slide sideways, as in other messengers: a new screen comes in from
 * the end edge over the current one, which moves a little the same way, and
 * going back reverses both, a predictive back gesture included. Every screen
 * draws its own background, so the one below does not show through.
 */
object ScreenTransitions {
    /** A navigation host whose screens move as described above. */
    @Composable
    fun Host(navController: NavHostController, startDestination: Any, builder: NavGraphBuilder.() -> Unit) {
        NavHost(
            navController,
            startDestination = startDestination,
            enterTransition = enter,
            exitTransition = exit,
            popEnterTransition = popEnter,
            popExitTransition = popExit,
            builder = builder,
        )
    }

    private val enter: AnimatedContentTransitionScope<NavBackStackEntry>.() -> EnterTransition = {
        slideIntoContainer(SlideDirection.Start, spec())
    }

    private val exit: AnimatedContentTransitionScope<NavBackStackEntry>.() -> ExitTransition = {
        slideOutOfContainer(SlideDirection.Start, spec()) { it / PARALLAX }
    }

    private val popEnter: AnimatedContentTransitionScope<NavBackStackEntry>.() -> EnterTransition = {
        slideIntoContainer(SlideDirection.End, spec()) { it / PARALLAX }
    }

    private val popExit: AnimatedContentTransitionScope<NavBackStackEntry>.() -> ExitTransition = {
        slideOutOfContainer(SlideDirection.End, spec())
    }

    private fun spec() = tween<IntOffset>(DURATION_MS, easing = Decelerate)

    private const val DURATION_MS = 280

    // The screen below moves by this part of the width.
    private const val PARALLAX = 4

    // Fast at first and settling softly.
    private val Decelerate = CubicBezierEasing(0.2f, 0f, 0f, 1f)
}
