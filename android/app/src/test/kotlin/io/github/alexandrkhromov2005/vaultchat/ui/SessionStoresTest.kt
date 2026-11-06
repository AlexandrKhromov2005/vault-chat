package io.github.alexandrkhromov2005.vaultchat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class SessionStoresTest {

    @Test
    fun anotherSessionGetsNewViewModelsAndClearsThePreviousOnes() {
        val stores = SessionStores()
        // Equal states of two sessions of the same user are still different sessions.
        val first = SessionState.SignedOut(SessionState.Reason.LOGOUT)
        val second = SessionState.SignedOut(SessionState.Reason.LOGOUT)

        val firstProbe = probe(stores.storeFor(first))
        assertSame(firstProbe, probe(stores.storeFor(first)))
        assertFalse(firstProbe.cleared)

        val secondProbe = probe(stores.storeFor(second))
        assertNotSame(firstProbe, secondProbe)
        assertTrue(firstProbe.cleared)
        assertFalse(secondProbe.cleared)
    }

    private fun probe(store: ViewModelStore): Probe {
        val owner = object : ViewModelStoreOwner {
            override val viewModelStore = store
        }
        return ViewModelProvider.create(owner, viewModelFactory { initializer { Probe() } })[Probe::class]
    }

    class Probe : ViewModel() {
        var cleared = false

        init {
            addCloseable { cleared = true }
        }
    }
}
