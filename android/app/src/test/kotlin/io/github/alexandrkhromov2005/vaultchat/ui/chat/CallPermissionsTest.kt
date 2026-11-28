package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.Manifest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CallPermissionsTest {
    @Test
    fun localNetworkIsRequestedOnlySinceApi36() {
        assertEquals(listOf(Manifest.permission.RECORD_AUDIO), callPermissions(35))
        assertEquals(
            listOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.ACCESS_LOCAL_NETWORK),
            callPermissions(36),
        )
        assertEquals(
            listOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.ACCESS_LOCAL_NETWORK),
            callPermissions(37),
        )
    }

    @Test
    fun missingPermissionsSkipGrantedOnes() {
        assertEquals(
            listOf(Manifest.permission.ACCESS_LOCAL_NETWORK),
            missingCallPermissions(37) { it == Manifest.permission.RECORD_AUDIO },
        )
        assertEquals(emptyList<String>(), missingCallPermissions(37) { true })
        assertEquals(
            listOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.ACCESS_LOCAL_NETWORK),
            missingCallPermissions(37) { false },
        )
    }

    @Test
    fun callProceedsWithoutLocalNetworkButNotWithoutMicrophone() {
        assertTrue(
            canProceedWithCall(
                mapOf(
                    Manifest.permission.RECORD_AUDIO to true,
                    Manifest.permission.ACCESS_LOCAL_NETWORK to false,
                ),
            ),
        )
        assertFalse(canProceedWithCall(mapOf(Manifest.permission.RECORD_AUDIO to false)))
        assertFalse(
            canProceedWithCall(
                mapOf(
                    Manifest.permission.RECORD_AUDIO to false,
                    Manifest.permission.ACCESS_LOCAL_NETWORK to true,
                ),
            ),
        )
    }

    @Test
    fun callProceedsWhenOnlyLocalNetworkWasRequested() {
        assertTrue(canProceedWithCall(mapOf(Manifest.permission.ACCESS_LOCAL_NETWORK to true)))
        assertTrue(canProceedWithCall(mapOf(Manifest.permission.ACCESS_LOCAL_NETWORK to false)))
    }
}
