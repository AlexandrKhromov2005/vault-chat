package io.github.alexandrkhromov2005.vaultchat.ui.theme

import android.content.ComponentName
import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.text.font.FontFamily
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
class PulseThemeTest {
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
    fun pulseUsesConsoleColorsAndMonospace() {
        var background = Color.Unspecified
        var primary = Color.Unspecified
        var bodyFont: FontFamily? = null
        compose.setContent {
            VaultChatTheme {
                background = MaterialTheme.colorScheme.background
                primary = MaterialTheme.colorScheme.primary
                bodyFont = MaterialTheme.typography.bodyLarge.fontFamily
            }
        }

        assertEquals(Color(0xFF0B0E0B), background)
        assertEquals(Color(0xFF46D98A), primary)
        assertEquals(FontFamily.Monospace, bodyFont)
    }
}
