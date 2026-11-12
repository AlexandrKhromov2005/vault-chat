package io.github.alexandrkhromov2005.vaultchat.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily

private val Pulse = darkColorScheme(
    primary = Color(0xFF46D98A),
    onPrimary = Color(0xFF0B0E0B),
    primaryContainer = Color(0xFF153522),
    onPrimaryContainer = Color(0xFF46D98A),
    secondary = Color(0xFF899189),
    onSecondary = Color(0xFF0B0E0B),
    secondaryContainer = Color(0xFF151A15),
    onSecondaryContainer = Color(0xFFC5CCC5),
    background = Color(0xFF0B0E0B),
    onBackground = Color(0xFFC5CCC5),
    surface = Color(0xFF0B0E0B),
    onSurface = Color(0xFFC5CCC5),
    surfaceVariant = Color(0xFF151A15),
    onSurfaceVariant = Color(0xFF899189),
    surfaceContainerLowest = Color(0xFF0B0E0B),
    surfaceContainerLow = Color(0xFF0F160F),
    surfaceContainer = Color(0xFF0F160F),
    surfaceContainerHigh = Color(0xFF151A15),
    surfaceContainerHighest = Color(0xFF202920),
    outline = Color(0xFF899189),
    outlineVariant = Color(0xFF202920),
    error = Color(0xFFFF7A72),
    onError = Color(0xFF0B0E0B),
    errorContainer = Color(0xFF4A1D18),
    onErrorContainer = Color(0xFFFF7A72),
)

private fun TextStyle.console() = copy(fontFamily = FontFamily.Monospace)

private val PulseTypography = with(Typography()) {
    Typography(
        displayLarge = displayLarge.console(),
        displayMedium = displayMedium.console(),
        displaySmall = displaySmall.console(),
        headlineLarge = headlineLarge.console(),
        headlineMedium = headlineMedium.console(),
        headlineSmall = headlineSmall.console(),
        titleLarge = titleLarge.console(),
        titleMedium = titleMedium.console(),
        titleSmall = titleSmall.console(),
        bodyLarge = bodyLarge.console(),
        bodyMedium = bodyMedium.console(),
        bodySmall = bodySmall.console(),
        labelLarge = labelLarge.console(),
        labelMedium = labelMedium.console(),
        labelSmall = labelSmall.console(),
    )
}

@Composable
fun VaultChatTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = Pulse,
        typography = PulseTypography,
        content = content,
    )
}
