package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.ComponentName
import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.RuleChain
import org.junit.runner.RunWith
import org.junit.runners.model.Statement
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf

@RunWith(RobolectricTestRunner::class)
class RoomNoticeRemovalTest {
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

    private class Screen : LifecycleOwner {
        val registry = LifecycleRegistry.createUnsafe(this)
        override val lifecycle: Lifecycle get() = registry
    }

    /** In room A the phone locks, a message notifies, and the user unlocks back into A. */
    @Test
    fun comingBackToTheRoomRemovesItsNotification() {
        val screen = Screen()
        screen.registry.currentState = Lifecycle.State.RESUMED
        var removed = 0
        compose.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides screen) {
                RemoveRoomNotice("room-a") { removed++ }
            }
        }
        compose.runOnIdle { assertEquals(1, removed) }

        compose.runOnIdle { screen.registry.currentState = Lifecycle.State.CREATED }
        compose.waitForIdle()
        assertEquals(1, removed)

        compose.runOnIdle { screen.registry.currentState = Lifecycle.State.RESUMED }
        compose.runOnIdle { assertEquals("the notification stayed after coming back to the room", 2, removed) }
    }

    @Test
    fun aScreenThatIsNotOnTopRemovesNothing() {
        val screen = Screen()
        screen.registry.currentState = Lifecycle.State.CREATED
        var removed = 0
        compose.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides screen) {
                RemoveRoomNotice("room-a") { removed++ }
            }
        }
        compose.runOnIdle { assertEquals(0, removed) }
    }
}
