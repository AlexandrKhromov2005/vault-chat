package io.github.alexandrkhromov2005.vaultchat.core.session

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.Gateway
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.LoginRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.RefreshTokenRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.RegisterRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.RequestProgress
import io.github.alexandrkhromov2005.vaultchat.core.net.ServerAddress
import io.github.alexandrkhromov2005.vaultchat.core.net.Success
import io.github.alexandrkhromov2005.vaultchat.core.net.TokenPair
import io.github.alexandrkhromov2005.vaultchat.core.net.bearer
import io.github.alexandrkhromov2005.vaultchat.core.net.call
import io.github.alexandrkhromov2005.vaultchat.core.net.callNoContent
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import retrofit2.Response
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException

/**
 * Every transition creates a new state object; screens scoped to a session
 * key on its identity (see SessionScope).
 */
sealed interface SessionState {
    data object Restoring : SessionState

    data class SignedOut(val reason: Reason) : SessionState

    data class SignedIn(val server: ServerAddress, val userId: String, val username: String) : SessionState

    enum class Reason { NONE, LOGOUT, EXPIRED }
}

/**
 * An access token and when it stops being valid, on the clock SessionManager
 * was given as `elapsed`.
 */
class AccessGrant(val token: String, val validUntil: Long)

/** The session could not be written to this device. */
class SessionStorageException(cause: Throwable) :
    ApiException(0, STORAGE, "session storage failed", cause = cause)

/**
 * The signed-in user's tokens.
 *
 * Refresh tokens rotate on every use, and Auth takes a later use of a
 * retired token for theft and ends every session of the user on every
 * device. Auth accepts a repeat of the token it just retired only within a
 * short window (PR #108, 30 s from the rotation). So:
 *
 * - refreshes run one at a time, and a refresh that has been sent is always
 *   completed and stored, even if its caller is cancelled;
 * - before a refresh is sent, the stored session records that the token may
 *   be retired ([StoredSession.refreshPendingSince]). If the answer is lost —
 *   a broken connection, a gateway error, an unreadable body, a full disk or
 *   a killed process — the same token is repeated only while a repeat is
 *   sure to reach Auth within its window ([RETRY_WINDOW_MS]). After that it
 *   is never sent to refresh again: the session is ended on the server with
 *   logout, which revokes the token's family without reuse detection, and
 *   only this device signs out;
 * - a failure before the request started to leave the device keeps the
 *   session and the token, however long it lasts.
 *
 * The window is measured on [elapsed], a monotonic clock that also runs in
 * deep sleep, so that setting the wall clock back cannot extend it. Only a
 * mark that survived a restart is aged on the wall clock, and one that seems
 * to come from the future is taken as expired.
 *
 * Each session has a generation. It changes whenever a session starts or
 * ends, so that the answer to a request made for an earlier session is
 * dropped instead of restoring or ending the current one, and a request is
 * never repeated with the token of a later session. Callers that work for
 * one signed-in user pass its [SessionState.SignedIn]: their requests fail
 * once that session has ended instead of using the next user's token.
 */
class SessionManager(
    private val store: SessionStore,
    private val gateway: Gateway,
    private val allowLocalCleartext: Boolean,
    private val now: () -> Long,
    private val elapsed: () -> Long,
) {
    private val mutableState = MutableStateFlow<SessionState>(SessionState.Restoring)
    val state: StateFlow<SessionState> = mutableState.asStateFlow()

    // Guards active, generation, the store and state. Held only briefly.
    private val stateLock = Mutex()

    // Serialises refreshes; never acquired while holding stateLock.
    private val refreshLock = Mutex()

    /** [pendingSince] and [accessValidUntil] are on the [elapsed] clock. */
    private class Active(
        val generation: Long,
        val signedIn: SessionState.SignedIn,
        val server: ServerAddress,
        val stored: StoredSession,
        val pendingSince: Long?,
        val accessToken: String?,
        val accessValidUntil: Long,
    )

    @Volatile
    private var active: Active? = null
    private var generation = 0L

    /** Loads the stored session. Works offline: the access token comes later. */
    suspend fun restore() {
        stateLock.withLock {
            if (mutableState.value != SessionState.Restoring) return
            val stored = try {
                store.load()
            } catch (e: Exception) {
                // The Keystore or the disk failed for now; the file is kept for
                // the next start, and signing in again replaces it.
                null
            }
            val server = stored?.let {
                try {
                    ServerAddress.parse(it.server, allowLocalCleartext)
                } catch (e: ServerAddress.InvalidException) {
                    null
                }
            }
            if (stored == null || server == null) {
                if (stored != null) runCatching { store.clear() }
                mutableState.value = SessionState.SignedOut(SessionState.Reason.NONE)
                return
            }
            // A mark from before the restart is aged on the wall clock; one from
            // the future fails mayRepeat and is abandoned.
            val pendingSince = stored.refreshPendingSince?.let { elapsed() - (now() - it) }
            val signedIn = SessionState.SignedIn(server, stored.userId, stored.username)
            active = Active(generation, signedIn, server, stored, pendingSince, accessToken = null, accessValidUntil = 0)
            mutableState.value = signedIn
        }
    }

    /** Creates an account; it does not sign in. */
    suspend fun register(server: ServerAddress, email: String, username: String, password: String, invitationToken: String) {
        call { gateway.api(server).register(RegisterRequest(email, username, password, invitationToken)) }
    }

    suspend fun login(server: ServerAddress, email: String, password: String) {
        val startedAt = stateLock.withLock { generation }
        val api = gateway.api(server)
        val pair = call { api.login(LoginRequest(email, password)) }
        val user = try {
            call { api.me(bearer(pair.body.accessToken)) }.body.also {
                if (it.userId != pair.body.userId) {
                    throw ApiException(200, ApiException.INVALID_RESPONSE, "identity of another user")
                }
            }
        } catch (e: Exception) {
            // The pair is unusable without the user's name; end it on the server.
            revoke(server, pair.body.refreshToken)
            throw e
        }
        val stored = StoredSession(server.toString(), pair.body.userId, user.username, pair.body.refreshToken)
        val failure: Exception? = stateLock.withLock {
            // A logout or another login happened while this one was under way.
            if (generation != startedAt) return@withLock sessionEnded()
            try {
                store.save(stored)
            } catch (e: Exception) {
                return@withLock SessionStorageException(e)
            }
            generation++
            val signedIn = SessionState.SignedIn(server, stored.userId, stored.username)
            active = Active(generation, signedIn, server, stored, null, pair.body.accessToken, accessValidUntil(pair))
            mutableState.value = signedIn
            null
        }
        if (failure != null) {
            revoke(server, pair.body.refreshToken)
            throw failure
        }
    }

    /**
     * Sends a request with a current access token. A 401 renews the token once
     * and repeats the request, within the same session only; a second 401 is
     * returned to the caller.
     */
    suspend fun <T : Any> authorized(request: suspend GatewayApi.(authorization: String) -> Response<T>): Success<T> =
        authorized(null, request)

    /** Like [authorized], but only while [session] is the signed-in session. */
    suspend fun <T : Any> authorized(
        session: SessionState.SignedIn?,
        request: suspend GatewayApi.(authorization: String) -> Response<T>,
    ): Success<T> {
        val (current, token) = credentials(session, RENEW_BEFORE_MS)
        val api = gateway.api(current.server)
        return try {
            call { api.request(bearer(token)) }
        } catch (e: ApiException) {
            if (!e.isUnauthenticated) throw e
            val (_, renewed) = refresh(rejected = token, from = current, minValidityMs = RENEW_BEFORE_MS)
            call { api.request(bearer(renewed)) }
        }
    }

    /** An access token that stays valid for more than [RENEW_BEFORE_MS]. */
    suspend fun accessToken(): String = credentials(null, RENEW_BEFORE_MS).second

    /**
     * An access token of [session] that stays valid for more than
     * [minValidityMs], for connections that hold a token, such as the
     * WebSocket. A token valid for less is renewed first.
     */
    suspend fun accessGrant(session: SessionState.SignedIn, minValidityMs: Long): AccessGrant {
        val (current, token) = credentials(session, minValidityMs)
        return current.grant(token)
    }

    /** Renews the access token of [session] after the server rejected [rejected]. */
    suspend fun renewAccess(session: SessionState.SignedIn, rejected: String): AccessGrant {
        val current = active?.takeIf { it.signedIn === session } ?: throw sessionEnded()
        val (renewed, token) = refresh(rejected = rejected, from = current, minValidityMs = RENEW_BEFORE_MS)
        return renewed.grant(token)
    }

    private fun Active.grant(token: String): AccessGrant =
        AccessGrant(token, if (token == accessToken) accessValidUntil else elapsed())

    private suspend fun credentials(session: SessionState.SignedIn?, minValidityMs: Long): Pair<Active, String> {
        val current = active ?: throw signedOut()
        if (session != null && current.signedIn !== session) throw sessionEnded()
        current.freshToken(minValidityMs)?.let { return current to it }
        return refresh(rejected = null, from = current, minValidityMs = minValidityMs)
    }

    private suspend fun refresh(rejected: String?, from: Active, minValidityMs: Long): Pair<Active, String> = refreshLock.withLock {
        val current = stateLock.withLock { active }
        if (current == null || current.generation != from.generation) throw sessionEnded()
        // Another caller may have refreshed while this one waited for the lock.
        current.freshToken(minValidityMs)?.takeIf { it != rejected }?.let { return current to it }
        // The outcome of a sent refresh must be handled even if the caller has
        // gone away meanwhile.
        withContext(NonCancellable) { rotate(current) }
    }

    private suspend fun rotate(start: Active): Pair<Active, String> {
        var current = start
        if (current.pendingSince != null && !current.mayRepeat()) abandon(current)
        // Whether an earlier attempt may have retired the token.
        var uncertain = current.pendingSince != null
        if (!uncertain) current = persist(current, current.stored.copy(refreshPendingSince = now()), elapsed())
        var attempt = 0
        while (true) {
            attempt++
            val progress = RequestProgress()
            val pair = try {
                call {
                    gateway.refreshApi(current.server).refresh(RefreshTokenRequest(current.stored.refreshToken), progress)
                }
            } catch (e: ApiException) {
                when (e.refreshOutcome(progress.sent)) {
                    Outcome.REJECTED -> {
                        endSession(current.generation, SessionState.Reason.EXPIRED)
                        revoke(current.server, current.stored.refreshToken)
                        throw e
                    }
                    Outcome.NOT_SENT -> if (!uncertain) {
                        persist(current, current.stored.copy(refreshPendingSince = null), null)
                        throw e
                    }
                    Outcome.UNKNOWN -> uncertain = true
                }
                // Repeat the same token at once, while the server still accepts it.
                if (attempt < MAX_ATTEMPTS && current.mayRepeat()) continue
                throw e
            }
            val next = current.stored.copy(refreshToken = pair.body.refreshToken, refreshPendingSince = null)
            val renewed = stateLock.withLock {
                if (generation != current.generation) throw sessionEnded()
                // If this write fails, the disk keeps the retired token marked as
                // pending, so after a restart it is not presented again.
                runCatching { store.save(next) }
                Active(current.generation, current.signedIn, current.server, next, null, pair.body.accessToken, accessValidUntil(pair))
                    .also { active = it }
            }
            return renewed to pair.body.accessToken
        }
    }

    /**
     * Whether a refresh started now is sure to reach Auth within its window:
     * the call deadline must end inside [RETRY_WINDOW_MS] of the mark.
     */
    private fun Active.mayRepeat(): Boolean {
        val since = elapsed() - (pendingSince ?: return true)
        return since in 0..RETRY_WINDOW_MS - Gateway.REFRESH_TIMEOUT_SECONDS * 1000
    }

    /** Stores [next] with its pending mark for the current session. */
    private suspend fun persist(current: Active, next: StoredSession, pendingSince: Long?): Active = stateLock.withLock {
        if (generation != current.generation) throw sessionEnded()
        try {
            store.save(next)
        } catch (e: Exception) {
            // The disk must not keep a token that may be retired without the
            // mark. Without the file the session goes on in memory and ends
            // with the process; if not even that works, no refresh is sent.
            if (next.refreshPendingSince != null) {
                try {
                    store.clear()
                } catch (cleared: Exception) {
                    throw SessionStorageException(cleared)
                }
            }
        }
        Active(current.generation, current.signedIn, current.server, next, pendingSince, current.accessToken, current.accessValidUntil)
            .also { active = it }
    }

    /**
     * Gives up a session whose token may have been retired too long ago for
     * Auth to accept it again: refreshing with it would look like theft.
     */
    private suspend fun abandon(current: Active): Nothing {
        endSession(current.generation, SessionState.Reason.EXPIRED)
        revoke(current.server, current.stored.refreshToken)
        throw sessionEnded()
    }

    /**
     * Forgets the session at once, then ends it on the server when that can be
     * reached. A refresh already under way finishes first, so that the server
     * sees the logout after it and revokes the session it was rotated into
     * rather than taking a later refresh for reuse.
     */
    suspend fun logout() {
        val ended = stateLock.withLock {
            val current = active
            generation++
            active = null
            runCatching { store.clear() }
            mutableState.value = SessionState.SignedOut(SessionState.Reason.LOGOUT)
            current
        } ?: return
        withContext(NonCancellable) {
            refreshLock.withLock { }
            revoke(ended.server, ended.stored.refreshToken)
        }
    }

    private suspend fun endSession(sessionGeneration: Long, reason: SessionState.Reason) {
        stateLock.withLock {
            if (generation != sessionGeneration) return
            generation++
            active = null
            runCatching { store.clear() }
            mutableState.value = SessionState.SignedOut(reason)
        }
    }

    private suspend fun revoke(server: ServerAddress, refreshToken: String) {
        try {
            withContext(NonCancellable) { callNoContent { gateway.api(server).logout(RefreshTokenRequest(refreshToken)) } }
        } catch (e: ApiException) {
            // An unreachable server only means the token stays valid until it expires.
        }
    }

    private fun Active.freshToken(minValidityMs: Long): String? =
        accessToken?.takeIf { accessValidUntil - elapsed() > minValidityMs }

    /**
     * When the access token stops being valid, on the [elapsed] clock. The
     * token's lifetime is measured against the response's Date header, so that
     * a phone whose clock is off neither refreshes on every request nor sends
     * expired tokens.
     */
    private fun accessValidUntil(pair: Success<TokenPair>): Long {
        val expiresAt = try {
            OffsetDateTime.parse(pair.body.accessExpiresAt).toInstant().toEpochMilli()
        } catch (e: DateTimeParseException) {
            return elapsed() + UNKNOWN_LIFETIME_MS
        }
        val lifetime = expiresAt - (pair.serverDateMillis ?: now())
        return elapsed() + lifetime.coerceAtLeast(0)
    }

    private enum class Outcome { REJECTED, NOT_SENT, UNKNOWN }

    /**
     * What a failed refresh means for the token. A failure before the request
     * started to leave the device, or a refusal by Gateway itself, cannot have
     * retired it; anything else, including 503 (Auth may have committed before
     * the connection to it broke), may have.
     */
    private fun ApiException.refreshOutcome(sent: Boolean): Outcome = when {
        // Auth refused the token, or it is malformed and can never work.
        status == 401 || status == 400 -> Outcome.REJECTED
        status == 0 && !sent -> Outcome.NOT_SENT
        // Rate limits, request timeouts and other refusals by Gateway itself.
        status in 402..499 -> Outcome.NOT_SENT
        else -> Outcome.UNKNOWN
    }

    companion object {
        /** Access tokens are renewed this long before they expire. */
        const val RENEW_BEFORE_MS = 60_000L

        /**
         * By when, after a refresh was marked, a repeat of its token must have
         * reached Auth. Auth's window is 30 s from the rotation, which is not
         * earlier than the mark; the margin covers clock drift and delivery.
         */
        const val RETRY_WINDOW_MS = 25_000L

        // The first attempt and one immediate repeat after a lost answer.
        private const val MAX_ATTEMPTS = 2

        // Assumed when the server's expiry cannot be read; a 401 renews earlier.
        private const val UNKNOWN_LIFETIME_MS = 2 * RENEW_BEFORE_MS

        private fun signedOut() = ApiException(401, "unauthenticated", "not signed in")

        private fun sessionEnded() = ApiException(401, "unauthenticated", "session ended")
    }
}
