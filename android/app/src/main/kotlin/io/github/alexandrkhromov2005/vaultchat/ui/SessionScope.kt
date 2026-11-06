package io.github.alexandrkhromov2005.vaultchat.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.viewmodel.compose.LocalViewModelStoreOwner
import androidx.lifecycle.viewmodel.compose.viewModel

/**
 * Holds the view models of one session state. Moving to another key clears
 * the previous ones: their requests are cancelled and none of their data is
 * shown to the next user. It lives in the activity's store, so the current
 * view models survive configuration changes.
 */
class SessionStores : ViewModel() {
    private var key: Any? = null
    private var store: ViewModelStore? = null

    /** The store for [newKey]; keys are compared by identity. */
    fun storeFor(newKey: Any): ViewModelStore {
        val current = store
        if (current != null && key === newKey) return current
        current?.clear()
        key = newKey
        return ViewModelStore().also { store = it }
    }

    override fun onCleared() {
        store?.clear()
        store = null
    }
}

/**
 * Provides [content] with view models that belong to [key] only. The owner
 * offers neither a default factory nor saved state, so view models here are
 * created with explicit factories and cannot use SavedStateHandle.
 */
@Composable
fun SessionScope(key: Any, content: @Composable () -> Unit) {
    val stores: SessionStores = viewModel()
    val store = stores.storeFor(key)
    val owner = remember(store) {
        object : ViewModelStoreOwner {
            override val viewModelStore = store
        }
    }
    CompositionLocalProvider(LocalViewModelStoreOwner provides owner, content = content)
}
