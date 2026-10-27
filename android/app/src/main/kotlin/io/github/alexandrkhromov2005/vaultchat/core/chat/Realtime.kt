package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.session.AccessGrant
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

/** Access tokens of one session for the WebSocket. */
interface AccessTokens {
    /** A token that stays valid for more than [minValidityMs]. */
    suspend fun grant(minValidityMs: Long): AccessGrant

    /** A new token after the server rejected [rejected]. */
    suspend fun renew(rejected: String): AccessGrant
}

enum class RealtimeState {
    /** Not running. */
    STOPPED,

    /** Opening the connection or waiting for `ready`. */
    CONNECTING,

    /** Authenticated; events arrive. */
    LIVE,

    /** The connection was lost; the next attempt is scheduled. */
    WAITING,

    /** The Gateway rejected the client's frames; retrying cannot help until the app changes. */
    FAILED,
}

/**
 * The WebSocket to the Gateway's event stream, following the client rules of
 * docs/chat/realtime.md. [run] connects, authenticates, re-authenticates
 * before the token expires and reconnects with [backoff] until it is
 * cancelled, the session ends or the Gateway reports a client error.
 *
 * [onReady] is called on the first `ready` of every connection, so that the
 * caller catches up on what it missed; [onFrame] receives the events that
 * follow. Socket callbacks arrive on OkHttp's threads and are handled in
 * order by [run]'s coroutine.
 */
class Realtime(
    private val userId: String,
    private val tokens: AccessTokens,
    private val open: (WebSocketListener) -> WebSocket,
    /** The clock of [AccessGrant.validUntil]. */
    private val elapsed: () -> Long,
    private val backoff: Backoff = Backoff(),
    private val onReady: () -> Unit,
    private val onFrame: (Frame) -> Unit,
) {
    private val mutableState = MutableStateFlow(RealtimeState.STOPPED)
    val state: StateFlow<RealtimeState> = mutableState.asStateFlow()

    private sealed interface Event {
        data object Opened : Event

        class Text(val text: String) : Event

        class Closed(val code: Int) : Event

        class Failed(val retryAfterSeconds: Long?) : Event

        data object ReauthDue : Event

        data object ReadyTimeout : Event
    }

    /** Why a connection ended. */
    private sealed interface End {
        class Closed(val code: Int, val token: String?) : End

        class Lost(val retryAfterSeconds: Long?) : End

        /** The session has ended; nothing more to do. */
        data object SignedOut : End

        /** A `ready` for another user. */
        data object WrongUser : End
    }

    /** Runs the connection until cancelled; the socket is closed on the way out. */
    suspend fun run() {
        backoff.reset()
        try {
            while (true) {
                if (mutableState.value != RealtimeState.WAITING) mutableState.value = RealtimeState.CONNECTING
                val delayMs = when (val end = connection()) {
                    End.SignedOut -> return
                    End.WrongUser -> {
                        mutableState.value = RealtimeState.FAILED
                        return
                    }
                    is End.Lost -> maxOf(backoff.next(), (end.retryAfterSeconds ?: 0) * 1000)
                    is End.Closed -> when (end.code) {
                        in CLIENT_ERRORS -> {
                            mutableState.value = RealtimeState.FAILED
                            return
                        }
                        UNAUTHORIZED -> renewAfterRejection(end.token) ?: return
                        else -> backoff.next()
                    }
                }
                mutableState.value = RealtimeState.WAITING
                delay(delayMs)
            }
        } finally {
            if (mutableState.value != RealtimeState.FAILED) mutableState.value = RealtimeState.STOPPED
        }
    }

    /**
     * After 4001, renews the token and returns the delay before reconnecting:
     * none after a renewal, the backoff after a network failure, null when
     * the session has ended.
     */
    private suspend fun renewAfterRejection(token: String?): Long? {
        if (token == null) return backoff.next()
        return try {
            tokens.renew(token)
            0
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            if (e.isUnauthenticated) null else backoff.next()
        }
    }

    private suspend fun connection(): End = coroutineScope {
        val inbox = Channel<Event>(Channel.UNLIMITED)
        val socket = open(object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                inbox.trySend(Event.Opened)
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                inbox.trySend(Event.Text(text))
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                inbox.trySend(Event.Closed(code))
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                inbox.trySend(Event.Closed(code))
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                val retryAfter = response?.header("Retry-After")?.trim()?.toLongOrNull()?.takeIf { it >= 0 }
                inbox.trySend(Event.Failed(retryAfter))
            }
        })
        var live = false
        var liveSince = 0L
        var sent: AccessGrant? = null
        var lastAuthAt = Long.MIN_VALUE
        var timer: Job? = null

        fun schedule(event: Event, delayMs: Long) {
            timer?.cancel()
            timer = launch {
                delay(delayMs)
                inbox.trySend(event)
            }
        }

        // Sends an auth frame; returns an end if the token cannot be had.
        suspend fun authenticate(): End? {
            val grant = try {
                tokens.grant(TOKEN_VALIDITY_MS)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException) {
                return if (e.isUnauthenticated) End.SignedOut else End.Lost(null)
            }
            sent = grant
            lastAuthAt = elapsed()
            socket.send(buildJsonObject { put("type", "auth"); put("token", grant.token) }.toString())
            return null
        }

        val end: End = try {
            schedule(Event.ReadyTimeout, CONNECT_TIMEOUT_MS)
            var result: End? = null
            while (result == null) {
                result = when (val event = inbox.receive()) {
                    Event.Opened -> authenticate()
                    is Event.Text -> {
                        when (val frame = if (event.text.length > MAX_FRAME_CHARS) null else Frame.parse(event.text)) {
                            null -> null
                            is Frame.Ready -> if (frame.userId != userId) {
                                End.WrongUser
                            } else {
                                if (!live) {
                                    live = true
                                    liveSince = elapsed()
                                    mutableState.value = RealtimeState.LIVE
                                    onReady()
                                }
                                // Renew before the token runs out, but never sooner than
                                // the Gateway allows.
                                val validUntil = sent?.validUntil ?: elapsed()
                                val at = maxOf(validUntil - REAUTH_BEFORE_MS, lastAuthAt + MIN_REAUTH_INTERVAL_MS)
                                schedule(Event.ReauthDue, maxOf(0, at - elapsed()))
                                null
                            }
                            else -> {
                                if (live) onFrame(frame)
                                null
                            }
                        }
                    }
                    Event.ReauthDue -> when (val failed = authenticate()) {
                        // Keep the connection; try again before the Gateway closes it with 4001.
                        is End.Lost -> {
                            schedule(Event.ReauthDue, MIN_REAUTH_INTERVAL_MS)
                            null
                        }
                        else -> failed
                    }
                    Event.ReadyTimeout -> if (live) null else End.Lost(null)
                    is Event.Closed -> End.Closed(event.code, sent?.token)
                    is Event.Failed -> End.Lost(event.retryAfterSeconds)
                }
            }
            result
        } finally {
            timer?.cancel()
            socket.close(NORMAL_CLOSURE, null)
            inbox.close()
        }
        // A connection that stayed up for a while starts the backoff over;
        // one that drops right after ready keeps growing it.
        if (live && elapsed() - liveSince >= STABLE_MS) backoff.reset()
        end
    }

    companion object {
        /** The token is renewed this long before it expires on the connection. */
        const val REAUTH_BEFORE_MS = 60_000L

        /** The Gateway closes connections whose auth frames come closer together. */
        const val MIN_REAUTH_INTERVAL_MS = 10_000L

        /**
         * A token sent on the connection must stay valid past the renewal point,
         * so that renewing hands over a fresh token rather than the same one.
         */
        const val TOKEN_VALIDITY_MS = REAUTH_BEFORE_MS + MIN_REAUTH_INTERVAL_MS

        /** From opening until `ready`; the Gateway itself allows 5 s for the auth frame. */
        const val CONNECT_TIMEOUT_MS = 15_000L

        /** How long a connection must have been live to reset the backoff. */
        const val STABLE_MS = 30_000L

        /** Event frames are a few kilobytes; a larger one is not parsed. */
        const val MAX_FRAME_CHARS = 64 * 1024

        const val NORMAL_CLOSURE = 1000
        const val UNAUTHORIZED = 4001

        /** The client itself is wrong: a binary frame, a protocol violation, a frame too large. */
        val CLIENT_ERRORS = setOf(1003, 1008, 1009)
    }
}
