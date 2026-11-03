package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.ChatDatabases
import io.github.alexandrkhromov2005.vaultchat.core.session.SessionState
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * One [ChatSession] per signed-in session. A new session ends the previous
 * one; signing out also deletes its data. Database files of sessions that are
 * neither current nor still open, such as those left behind by a killed
 * process, are deleted whenever the session changes.
 *
 * Opening a session, wiping data and deleting files all hold this object's
 * lock, and decide what to touch only once they hold it: work queued for an
 * earlier state must not reach the data of a session that has opened since.
 * They are brief, and sessions change rarely, so a screen asking for its
 * session is not held up for long.
 */
class ChatSessions(
    private val databases: ChatDatabases,
    private val deps: ChatSessionDeps,
    private val appScope: CoroutineScope,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) {
    private var current: ChatSession? = null
    private var state: StateFlow<SessionState>? = null

    // Ended sessions whose database some screen still reads from.
    private val open = mutableSetOf<ChatSession>()

    /** Follows [state] for the life of the process. */
    fun follow(state: StateFlow<SessionState>) {
        synchronized(this) { this.state = state }
        appScope.launch {
            state.collect { session ->
                when (session) {
                    // The stored session is not known yet: its file must stay.
                    SessionState.Restoring -> return@collect
                    is SessionState.SignedIn -> get(session)
                    is SessionState.SignedOut -> endCurrent(wipe = true)
                }
                cleanUp()
            }
        }
    }

    /**
     * The chat of the current session once the stored session is known, or
     * null when nobody is signed in.
     */
    suspend fun awaitCurrent(): ChatSession? {
        val state = synchronized(this) { state } ?: return null
        val signedIn = state.first { it != SessionState.Restoring } as? SessionState.SignedIn ?: return null
        return get(signedIn)
    }

    /** The chat of [signedIn], created when first asked for. */
    @Synchronized
    fun get(signedIn: SessionState.SignedIn): ChatSession {
        current?.takeIf { it.signedIn === signedIn }?.let { return it }
        val name = ChatDatabases.fileName(signedIn.server.toString(), signedIn.userId)
        val session = ChatSession(
            signedIn, name, databases.open(signedIn.server.toString(), signedIn.userId), databases.files(name), deps, ::released,
        )
        open += session
        if (state?.value !== signedIn) {
            // A screen asked for a session that has ended meanwhile: it gets
            // one that does nothing, and the current session is untouched.
            session.end(appScope, io)
            return session
        }
        // A new session of the same user and server keeps its data.
        endCurrent(wipe = current?.databaseName != name)
        current = session
        return session
    }

    @Synchronized
    private fun endCurrent(wipe: Boolean) {
        if (wipe) current?.end(appScope, io, ::wipeUnlessReopened) else current?.end(appScope, io)
        current = null
    }

    /** The same user's new session owns the data by now, and keeps it. */
    @Synchronized
    private fun wipeUnlessReopened(ended: ChatSession) {
        if (current?.databaseName != ended.databaseName) ended.clearData()
    }

    private fun released(session: ChatSession) {
        appScope.launch(io) {
            // Queries that screens started just before they let go may still run.
            delay(CLOSE_GRACE_MS)
            session.close()
            synchronized(this@ChatSessions) { open -= session }
            cleanUp()
        }
    }

    private fun cleanUp() {
        appScope.launch(io) {
            synchronized(this@ChatSessions) {
                runCatching { databases.deleteAllExcept(open.map { it.databaseName }.toSet()) }
            }
        }
    }

    private companion object {
        const val CLOSE_GRACE_MS = 2_000L
    }
}
