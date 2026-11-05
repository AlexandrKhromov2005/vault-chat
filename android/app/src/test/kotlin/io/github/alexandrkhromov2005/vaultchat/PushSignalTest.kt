package io.github.alexandrkhromov2005.vaultchat

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PushSignalTest {
    private val sync = mapOf("type" to "sync", "room_id" to "00000000-0000-4000-8000-000000000001")

    @Test
    fun aSignalWakesASyncWithoutASocket() {
        assertTrue(PushSignal.wakesSync(sync, socketLive = false))
    }

    @Test
    fun aSignalIsIgnoredWhileTheSocketIsLive() {
        assertFalse("a live socket got a sync for every push", PushSignal.wakesSync(sync, socketLive = true))
    }

    @Test
    fun anythingElseIsIgnored() {
        assertFalse(PushSignal.wakesSync(emptyMap(), socketLive = false))
        assertFalse(PushSignal.wakesSync(mapOf("type" to "other"), socketLive = false))
        assertFalse(PushSignal.wakesSync(mapOf("room_id" to "x"), socketLive = false))
    }
}
