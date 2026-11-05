package io.github.alexandrkhromov2005.vaultchat.core.chat

import org.junit.Assert.assertEquals
import org.junit.Test

class RateGateTest {
    private var now = 0L
    private val gate = RateGate { now }

    @Test
    fun aRetryAfterOfZeroStillPausesForASecond() {
        // Otherwise a proxy answering 429 with Retry-After: 0 makes a busy loop.
        gate.pause(0)

        assertEquals(1_000, gate.remaining())
    }

    @Test
    fun anExcessiveRetryAfterIsCapped() {
        gate.pause(10_000_000)

        assertEquals(RateGate.MAX_PAUSE_MS, gate.remaining())
    }

    @Test
    fun withoutRetryAfterTheServerDefaultApplies() {
        gate.pause(null)

        assertEquals(30_000, gate.remaining())
    }
}
