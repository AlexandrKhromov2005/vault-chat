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

    /** Pauses requests for [retryAfterSeconds], or 30 s without it. */
    @Synchronized
    fun pause(retryAfterSeconds: Long?) {
        pausedUntil = maxOf(pausedUntil, clock() + (retryAfterSeconds ?: DEFAULT_RETRY_AFTER_SECONDS) * 1000)
    }

    private companion object {
        const val DEFAULT_RETRY_AFTER_SECONDS = 30L
    }
}
