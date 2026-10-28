package io.github.alexandrkhromov2005.vaultchat.core.chat

/**
 * The Chat service's rules for message text (internal/chat/validator), checked
 * before a message is queued so that the server does not reject it later.
 * Text is never trimmed or normalised: the server stores it as given.
 */
object MessageText {
    /** Why a text cannot be sent. */
    enum class Problem { EMPTY, TOO_LONG, INVALID_CHARACTER }

    const val MAX_BYTES = 16 * 1024

    /** The problem with [text], or null if it can be sent. */
    fun check(text: String): Problem? {
        var codePoints = 0
        var bytes = 0
        var visible = false
        var index = 0
        while (index < text.length) {
            val char = text[index]
            val codePoint = when {
                char.isHighSurrogate() && index + 1 < text.length && text[index + 1].isLowSurrogate() ->
                    Character.toCodePoint(char, text[index + 1])
                // An unpaired surrogate is not valid UTF-8.
                char.isSurrogate() -> return Problem.INVALID_CHARACTER
                else -> char.code
            }
            index += Character.charCount(codePoint)
            if (isControl(codePoint) && codePoint != '\n'.code && codePoint != '\t'.code) return Problem.INVALID_CHARACTER
            if (!isSpace(codePoint)) visible = true
            codePoints++
            bytes += utf8Length(codePoint)
        }
        return when {
            !visible -> Problem.EMPTY
            codePoints > MAX_TEXT_CODE_POINTS || bytes > MAX_BYTES -> Problem.TOO_LONG
            else -> null
        }
    }

    /** The length the server counts: Unicode code points. */
    fun length(text: String): Int = text.codePointCount(0, text.length)

    // Go's unicode.IsControl: C0, DEL and C1.
    private fun isControl(codePoint: Int) = codePoint < 0x20 || codePoint in 0x7f..0x9f

    // Go's unicode.IsSpace, which strings.TrimSpace uses.
    private fun isSpace(codePoint: Int) = when (codePoint) {
        '\t'.code, '\n'.code, 0x0b, 0x0c, '\r'.code, ' '.code, 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000 -> true
        else -> codePoint in 0x2000..0x200a
    }

    private fun utf8Length(codePoint: Int) = when {
        codePoint < 0x80 -> 1
        codePoint < 0x800 -> 2
        codePoint < 0x10000 -> 3
        else -> 4
    }
}
