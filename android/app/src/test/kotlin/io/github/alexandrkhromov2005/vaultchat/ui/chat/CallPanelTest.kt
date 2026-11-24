package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.ComponentName
import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallConnection
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallRole
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallView
import io.github.alexandrkhromov2005.vaultchat.ui.theme.VaultChatTheme
import org.junit.Rule
import org.junit.Test
import org.junit.rules.RuleChain
import org.junit.runner.RunWith
import org.junit.runners.model.Statement
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf

@RunWith(RobolectricTestRunner::class)
class CallPanelTest {
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

    private val context = ApplicationProvider.getApplicationContext<Context>()

    private fun show(view: CallView) {
        compose.setContent { VaultChatTheme { CallStatus(view) } }
        compose.waitForIdle()
    }

    private fun view(state: String, connection: CallConnection = CallConnection.UNKNOWN) = CallView(
        callId = "call-1", roomId = "room-1", state = state, role = CallRole.CALLER,
        owned = true, remote = state == "active", connection = connection,
    )

    private fun shown(resource: Int) {
        compose.onNodeWithText(context.getString(resource)).assertExists()
    }

    @Test
    fun ringingExplainsThatInvitationWasDelivered() {
        show(view("ringing"))

        shown(R.string.call_detail_ringing_caller)
        compose.onNodeWithText(context.getString(R.string.call_progress, 2)).assertExists()
    }

    @Test
    fun activeCallShowsAudioAndRelayTransport() {
        show(view("active", CallConnection.RELAY))

        shown(R.string.call_detail_active)
        shown(R.string.call_connection_relay)
        compose.onNodeWithText(context.getString(R.string.call_progress, 4)).assertExists()
    }

    @Test
    fun interruptedCallShowsRecoveryInsteadOfClaimingHealthyMedia() {
        show(view("connecting", CallConnection.RECOVERING))

        shown(R.string.call_connection_recovering)
        compose.onNodeWithText(context.getString(R.string.call_progress, 3)).assertExists()
    }
}
