package io.github.alexandrkhromov2005.vaultchat

import android.app.Application
import kotlinx.coroutines.launch

class VaultChatApplication : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
        container.appScope.launch { container.session.restore() }
    }
}
