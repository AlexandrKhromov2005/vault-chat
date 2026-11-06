package io.github.alexandrkhromov2005.vaultchat.core.chat

import kotlin.random.Random

/**
 * Reconnection delays: exponential with jitter, so that clients that lost
 * the same Gateway instance do not all come back at the same moment.
 */
class Backoff(
    private val baseMs: Long = 500,
    private val maxMs: Long = 30_000,
    private val random: () -> Double = { Random.nextDouble() },
) {
    private var attempt = 0

    /** The delay before the next attempt: between half and all of base·2^attempt, at most max. */
    fun next(): Long {
        val full = if (attempt >= 30) maxMs else minOf(maxMs, baseMs shl attempt)
        attempt++
        return (full * (0.5 + random() / 2)).toLong()
    }

    fun reset() {
        attempt = 0
    }
}
