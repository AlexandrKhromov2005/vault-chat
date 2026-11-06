package io.github.alexandrkhromov2005.vaultchat.ui

import android.content.ComponentName
import android.content.Context
import androidx.activity.BackEventCompat
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedDispatcher
import androidx.activity.compose.LocalOnBackPressedDispatcherOwner
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.width
import androidx.navigation.NavHostController
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.test.core.app.ApplicationProvider
import kotlinx.serialization.Serializable
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.RuleChain
import org.junit.runner.RunWith
import org.junit.runners.model.Statement
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

@Serializable
internal object FirstRoute

@Serializable
internal object SecondRoute

/** The back gesture has transitions of its own; they must slide like everything else. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ScreenTransitionsTest {
    private val compose = createComposeRule()

    @get:Rule
    val rules: RuleChain = RuleChain.outerRule { base, _ ->
        object : Statement() {
            override fun evaluate() {
                val context = ApplicationProvider.getApplicationContext<Context>()
                shadowOf(context.packageManager).addActivityIfNotPresent(ComponentName(context, ComponentActivity::class.java))
                base.evaluate()
            }
        }
    }.around(compose)

    @Test
    fun theScreenFollowsTheBackGestureSidewaysAtFullSize() {
        lateinit var navController: NavHostController
        lateinit var dispatcher: OnBackPressedDispatcher
        compose.setContent {
            navController = rememberNavController()
            dispatcher = LocalOnBackPressedDispatcherOwner.current!!.onBackPressedDispatcher
            ScreenTransitions.Host(navController, startDestination = FirstRoute) {
                composable<FirstRoute> { Box(Modifier.fillMaxSize().testTag("first")) }
                composable<SecondRoute> { Box(Modifier.fillMaxSize().testTag("second")) }
            }
        }
        compose.runOnIdle { navController.navigate(SecondRoute) }
        compose.waitForIdle()
        val full = compose.onNodeWithTag("second").getBoundsInRoot()

        compose.runOnIdle {
            dispatcher.dispatchOnBackStarted(BackEventCompat(0f, 100f, 0f, BackEventCompat.EDGE_LEFT))
            dispatcher.dispatchOnBackProgressed(BackEventCompat(200f, 100f, 0.5f, BackEventCompat.EDGE_LEFT))
        }
        compose.waitForIdle()
        val during = compose.onNodeWithTag("second").getBoundsInRoot()

        // By default the screen shrinks towards the centre.
        assertEquals(full.width.value, during.width.value, 1f)
        assertTrue("the screen did not move: $during", during.left > full.left + TOLERANCE)
    }

    private companion object {
        val TOLERANCE: Dp = 1.dp
    }
}
