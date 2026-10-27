package io.github.alexandrkhromov2005.vaultchat.core.chat

import kotlinx.coroutines.delay
import kotlin.math.ceil

/**
 * A token bucket for the requests of sync, below the Gateway's own limit per
 * user, so that the user's own requests still find room.
 *
 * Background work waits for a token. A request the user is waiting for, such
 * as opening a room, takes one at once; it may run the bucket into debt of at
 * most [burst] tokens, which background work then waits out.
 */
class RequestBudget(perMinute: Int, private val burst: Int, private val clock: () -> Long) {
    private val msPerToken = 60_000.0 / perMinute
    private var tokens = burst.toDouble()
    private var updatedAt = clock()

    /** Takes a token for background work, waiting until there is one. */
    suspend fun take() {
        while (true) {
            val wait = tryTake()
            if (wait <= 0) return
            delay(wait)
        }
    }

    /** Takes a token for a request the user waits for, without waiting. */
    @Synchronized
    fun takeNow() {
        refill()
        tokens = maxOf(tokens - 1, -burst.toDouble())
    }

    /** Takes a token and returns 0, or returns how long to wait for one. */
    @Synchronized
    private fun tryTake(): Long {
        refill()
        if (tokens >= 1) {
            tokens -= 1
            return 0
        }
        return ceil((1 - tokens) * msPerToken).toLong().coerceAtLeast(1)
    }

    private fun refill() {
        val now = clock()
        tokens = minOf(burst.toDouble(), tokens + (now - updatedAt).coerceAtLeast(0) / msPerToken)
        updatedAt = now
    }
}
