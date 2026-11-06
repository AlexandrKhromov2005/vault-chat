package io.github.alexandrkhromov2005.vaultchat.core.chat

/**
 * A username prefix as `GET /api/v1/users` accepts it (docs/api/gateway.yaml),
 * checked before a search is sent.
 */
object UserQuery {
    private val PATTERN = Regex("[A-Za-z][A-Za-z0-9_-]{1,31}")

    /** Why a query is not searched for. */
    enum class Problem { TOO_SHORT, INVALID }

    /** The query to send: [input] without surrounding whitespace. */
    fun normalize(input: String): String = input.trim()

    /** The problem with the normalized [query], or null if it can be searched for. */
    fun check(query: String): Problem? = when {
        query.length < 2 -> Problem.TOO_SHORT
        !PATTERN.matches(query) -> Problem.INVALID
        else -> null
    }
}

/**
 * The Chat service's rules for a channel name (internal/chat/validator): 1–80
 * code points, no control characters, no surrounding whitespace. Surrounding
 * whitespace is removed before the check instead of being rejected.
 */
object ChannelName {
    const val MAX_CODE_POINTS = 80

    enum class Problem { EMPTY, TOO_LONG, INVALID_CHARACTER }

    fun normalize(input: String): String = trimSpace(input)

    /** The problem with the normalized [name], or null if a channel can be created with it. */
    fun check(name: String): Problem? {
        if (name.isEmpty()) return Problem.EMPTY
        var index = 0
        var codePoints = 0
        while (index < name.length) {
            val char = name[index]
            val codePoint = when {
                char.isHighSurrogate() && index + 1 < name.length && name[index + 1].isLowSurrogate() ->
                    Character.toCodePoint(char, name[index + 1])
                // An unpaired surrogate is not valid UTF-8.
                char.isSurrogate() -> return Problem.INVALID_CHARACTER
                else -> char.code
            }
            if (isControl(codePoint)) return Problem.INVALID_CHARACTER
            index += Character.charCount(codePoint)
            codePoints++
        }
        return if (codePoints > MAX_CODE_POINTS) Problem.TOO_LONG else null
    }
}
