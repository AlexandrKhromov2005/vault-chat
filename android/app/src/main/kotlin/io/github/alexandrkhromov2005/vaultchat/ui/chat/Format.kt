package io.github.alexandrkhromov2005.vaultchat.ui.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.platform.LocalLocale
import androidx.compose.ui.res.stringResource
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ConnectionStatus
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.RoomEntity
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.time.temporal.ChronoUnit
import java.util.Locale

// Times are microseconds since the epoch, as stored. They are shown in the
// phone's zone and in the locale of the composition, which follows a change
// of the system language.

private fun zoned(epochMicros: Long) =
    Instant.ofEpochSecond(Math.floorDiv(epochMicros, 1_000_000L), Math.floorMod(epochMicros, 1_000_000L) * 1_000L)
        .atZone(ZoneId.systemDefault())

fun epochDay(epochMicros: Long): Long = zoned(epochMicros).toLocalDate().toEpochDay()

/** A message's time of day. */
fun formatTime(epochMicros: Long, locale: Locale): String =
    zoned(epochMicros).format(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale))

/** The time in the room list: today's time, a weekday within a week, otherwise the date. */
fun formatListTime(epochMicros: Long, locale: Locale, today: LocalDate = LocalDate.now()): String {
    val time = zoned(epochMicros)
    val date = time.toLocalDate()
    val days = ChronoUnit.DAYS.between(date, today)
    val pattern = when {
        days <= 0L -> return formatTime(epochMicros, locale)
        days < 7 -> "EE"
        date.year == today.year -> "d MMM"
        else -> "dd.MM.yy"
    }
    return time.format(DateTimeFormatter.ofPattern(pattern, locale))
}

/** The heading of a day in a conversation. */
@Composable
@ReadOnlyComposable
fun formatDay(epochDay: Long, today: LocalDate = LocalDate.now()): String {
    val date = LocalDate.ofEpochDay(epochDay)
    return when (ChronoUnit.DAYS.between(date, today)) {
        0L -> stringResource(R.string.day_today)
        1L -> stringResource(R.string.day_yesterday)
        else -> date.format(
            DateTimeFormatter.ofPattern(if (date.year == today.year) "d MMMM" else "d MMMM yyyy", LocalLocale.current.platformLocale),
        )
    }
}

/** The name of a room: the channel name or the dialog peer's name. */
@Composable
@ReadOnlyComposable
fun roomTitle(room: RoomEntity, peerName: String?): String = when (room.kind) {
    RoomEntity.CHANNEL -> room.name?.takeIf { it.isNotBlank() } ?: stringResource(R.string.room_kind_channel)
    RoomEntity.DIRECT -> peerName ?: stringResource(R.string.room_kind_direct)
    else -> room.name ?: stringResource(R.string.room_kind_other)
}

@Composable
@ReadOnlyComposable
fun roomKind(room: RoomEntity): String = stringResource(
    when {
        room.kind == RoomEntity.DIRECT -> R.string.room_kind_direct
        room.kind != RoomEntity.CHANNEL -> R.string.room_kind_other
        room.isPrivate -> R.string.room_kind_private_channel
        else -> R.string.room_kind_channel
    },
)

/** The connection state, or null when everything is up to date. */
@Composable
@ReadOnlyComposable
fun connectionText(status: ConnectionStatus): String? = when (status) {
    ConnectionStatus.ONLINE -> null
    ConnectionStatus.UPDATING -> stringResource(R.string.connection_updating)
    ConnectionStatus.CONNECTING -> stringResource(R.string.connection_connecting)
    ConnectionStatus.OFFLINE -> stringResource(R.string.connection_no_network)
    ConnectionStatus.UNREACHABLE -> stringResource(R.string.connection_unreachable)
}

/** The first letter of a name, for its avatar. */
fun initial(name: String): String {
    val trimmed = name.trim()
    if (trimmed.isEmpty()) return "?"
    val end = trimmed.offsetByCodePoints(0, 1)
    return trimmed.substring(0, end).uppercase()
}
