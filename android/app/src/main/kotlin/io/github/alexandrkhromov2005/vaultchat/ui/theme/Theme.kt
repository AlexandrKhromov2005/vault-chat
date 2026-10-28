package io.github.alexandrkhromov2005.vaultchat.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

// One accent, the web client's blue, on neutral surfaces. Dynamic color is
// not used, so the app looks the same on every phone.

private val Light = lightColorScheme(
    primary = Color(0xFF2F6FDF),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFDCE6FB),
    onPrimaryContainer = Color(0xFF0B2A63),
    secondary = Color(0xFF4F5B70),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFE2E6EE),
    onSecondaryContainer = Color(0xFF1D2330),
    background = Color(0xFFF7F8FA),
    onBackground = Color(0xFF1D2330),
    surface = Color(0xFFF7F8FA),
    onSurface = Color(0xFF1D2330),
    surfaceVariant = Color(0xFFE6E9EF),
    onSurfaceVariant = Color(0xFF586173),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFF1F3F6),
    surfaceContainer = Color(0xFFECEEF2),
    surfaceContainerHigh = Color(0xFFE6E9EE),
    surfaceContainerHighest = Color(0xFFE0E3E9),
    outline = Color(0xFF8A93A3),
    outlineVariant = Color(0xFFD9DDE5),
    error = Color(0xFFC0362C),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFFBE3E0),
    onErrorContainer = Color(0xFF5C120C),
)

private val Dark = darkColorScheme(
    primary = Color(0xFFA9C4FF),
    onPrimary = Color(0xFF0A2F6E),
    primaryContainer = Color(0xFF1E4FA8),
    onPrimaryContainer = Color(0xFFDCE6FB),
    secondary = Color(0xFFB9C2D3),
    onSecondary = Color(0xFF242B38),
    secondaryContainer = Color(0xFF353C4A),
    onSecondaryContainer = Color(0xFFE2E6EE),
    background = Color(0xFF111318),
    onBackground = Color(0xFFE3E5EB),
    surface = Color(0xFF111318),
    onSurface = Color(0xFFE3E5EB),
    surfaceVariant = Color(0xFF2A2E37),
    onSurfaceVariant = Color(0xFFB3B9C6),
    surfaceContainerLowest = Color(0xFF0C0E12),
    surfaceContainerLow = Color(0xFF191B21),
    surfaceContainer = Color(0xFF1D2026),
    surfaceContainerHigh = Color(0xFF272A31),
    surfaceContainerHighest = Color(0xFF32353C),
    outline = Color(0xFF7D8494),
    outlineVariant = Color(0xFF3A3F4A),
    error = Color(0xFFFFB4AB),
    onError = Color(0xFF690005),
    errorContainer = Color(0xFF93000A),
    onErrorContainer = Color(0xFFFFDAD6),
)

@Composable
fun VaultChatTheme(darkTheme: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (darkTheme) Dark else Light, content = content)
}
