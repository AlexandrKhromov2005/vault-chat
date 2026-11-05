package io.github.alexandrkhromov2005.vaultchat.core.system

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Whether any activity of the app is visible. Create on the main thread. */
class AppForeground : DefaultLifecycleObserver {
    private val mutableVisible = MutableStateFlow(false)
    val visible: StateFlow<Boolean> = mutableVisible.asStateFlow()

    init {
        ProcessLifecycleOwner.get().lifecycle.addObserver(this)
    }

    override fun onStart(owner: LifecycleOwner) {
        mutableVisible.value = true
    }

    override fun onStop(owner: LifecycleOwner) {
        mutableVisible.value = false
    }
}

/**
 * The default network, or null without one. A change of network (Wi-Fi to
 * mobile data) gives a new value, so that connections are reopened at once
 * instead of waiting for the old one to time out. Validation is not
 * required: the system's connectivity check fails on some networks that
 * reach the Gateway well.
 */
class NetworkMonitor(context: Context) {
    private val mutableNetwork = MutableStateFlow<Network?>(null)
    val network: StateFlow<Network?> = mutableNetwork.asStateFlow()

    init {
        val manager = context.getSystemService(ConnectivityManager::class.java)
        manager.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
            override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) {
                val usable = capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                mutableNetwork.value = if (usable) network else null
            }

            override fun onLost(network: Network) {
                mutableNetwork.compareAndSet(network, null)
            }
        })
    }
}
