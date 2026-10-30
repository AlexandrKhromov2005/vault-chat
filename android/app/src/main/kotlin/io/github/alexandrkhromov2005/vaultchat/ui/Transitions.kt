package io.github.alexandrkhromov2005.vaultchat.ui

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.AnimatedContentTransitionScope.SlideDirection
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.tween
import androidx.compose.ui.unit.IntOffset
import androidx.navigation.NavBackStackEntry

/**
 * Screens slide sideways, as in other messengers: a new screen comes in from
 * the end edge over the current one, which moves a little the same way, and
 * going back reverses both. The back gesture has transitions of its own in
 * navigation-compose (by default the screen shrinks to the centre); here
 * they are the same slides, following the finger evenly. Every screen draws
 * its own background, so the one below does not show through.
 */
object ScreenTransitions {
    val enter: AnimatedContentTransitionScope<NavBackStackEntry>.() -> EnterTransition = {
        slideIntoContainer(SlideDirection.Start, spec())
    }

    val exit: AnimatedContentTransitionScope<NavBackStackEntry>.() -> ExitTransition = {
        slideOutOfContainer(SlideDirection.Start, spec()) { it / PARALLAX }
    }

    val popEnter: AnimatedContentTransitionScope<NavBackStackEntry>.() -> EnterTransition = {
        slideIntoContainer(SlideDirection.End, spec()) { it / PARALLAX }
    }

    val popExit: AnimatedContentTransitionScope<NavBackStackEntry>.() -> ExitTransition = {
        slideOutOfContainer(SlideDirection.End, spec())
    }

    val gestureEnter: AnimatedContentTransitionScope<NavBackStackEntry>.(Int) -> EnterTransition = {
        slideIntoContainer(SlideDirection.End, spec(LinearEasing)) { it / PARALLAX }
    }

    val gestureExit: AnimatedContentTransitionScope<NavBackStackEntry>.(Int) -> ExitTransition = {
        slideOutOfContainer(SlideDirection.End, spec(LinearEasing))
    }

    private fun spec(easing: Easing = Decelerate) = tween<IntOffset>(DURATION_MS, easing = easing)

    private const val DURATION_MS = 280

    // The screen below moves by this part of the width.
    private const val PARALLAX = 4

    // Fast at first and settling softly.
    private val Decelerate = CubicBezierEasing(0.2f, 0f, 0f, 1f)
}
