package io.github.alexandrkhromov2005.vaultchat.core.chat

/**
 * The pause after a 429. The Gateway's rate limit counts every request of the
 * user, so sync and sending wait for the same pause.
 */
class RateGate(private val clock: () -> Long) {
    @Volatile
    private var pausedUntil = 0L

    /** Milliseconds until requests may be sent again; zero or less when they may. */
    fun remaining(): Long = pausedUntil - clock()

    /**
     * Pauses requests for [retryAfterSeconds], or 30 s without it. The pause
     * is at least a second, so that a Retry-After of 0 from a proxy cannot make
     * a busy loop, and at most [MAX_PAUSE_MS], so that a bogus one cannot stop
     * the app for hours.
     */
    @Synchronized
    fun pause(retryAfterSeconds: Long?) {
        val seconds = (retryAfterSeconds ?: DEFAULT_RETRY_AFTER_SECONDS).coerceIn(1, MAX_PAUSE_MS / 1000)
        pausedUntil = maxOf(pausedUntil, clock() + seconds * 1000)
    }

    companion object {
        private const val DEFAULT_RETRY_AFTER_SECONDS = 30L
        const val MAX_PAUSE_MS = 5 * 60 * 1000L
    }
}
