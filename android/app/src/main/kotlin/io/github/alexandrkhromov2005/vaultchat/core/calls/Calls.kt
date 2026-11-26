package io.github.alexandrkhromov2005.vaultchat.core.calls

import io.github.alexandrkhromov2005.vaultchat.core.calls.CallRole.CALLEE
import io.github.alexandrkhromov2005.vaultchat.core.calls.CallRole.CALLER
import io.github.alexandrkhromov2005.vaultchat.core.chat.ChatRequests
import io.github.alexandrkhromov2005.vaultchat.core.chat.Frame
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.CallCommand
import io.github.alexandrkhromov2005.vaultchat.core.net.CallDto
import io.github.alexandrkhromov2005.vaultchat.core.net.CallSignalRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.CreateCallRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.EmptyCallBody
import io.github.alexandrkhromov2005.vaultchat.core.net.IceServerDto
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeout
import java.util.UUID

enum class CallRole { CALLER, CALLEE }
enum class CallConnection { UNKNOWN, DIRECT, RELAY, RECOVERING }

data class CallView(
    val callId: String? = null,
    val roomId: String,
    val state: String,
    val role: CallRole,
    val owned: Boolean,
    val pending: Boolean = false,
    val remote: Boolean = false,
    val connection: CallConnection = CallConnection.UNKNOWN,
    val error: String? = null,
)

interface CallMedia {
    suspend fun prepare()
    suspend fun start(role: CallRole, iceServers: List<IceServerDto>, listener: CallMediaListener)
    suspend fun receive(kind: String, payload: String)
    suspend fun usesRelay(): Boolean = false
    suspend fun audioBytes(): Pair<Long, Long> = 0L to 0L
    suspend fun restart(iceServers: List<IceServerDto>) {}
    fun close()
}

interface CallMediaListener {
    suspend fun signal(kind: String, payload: String)
    fun connected()
    fun remote()
    fun connection(value: CallConnection) {}
    suspend fun refreshTurn(): List<IceServerDto> = emptyList()
    fun failed()
}

interface CallMediaFactory {
    fun create(): CallMedia
}

/** One session's call: server state, signaling reconciliation and media cleanup. */
class Calls(
    private val me: String,
    private val requests: ChatRequests,
    private val mediaFactory: CallMediaFactory,
    private val foreground: (Boolean) -> Unit = {},
    dispatcher: kotlinx.coroutines.CoroutineDispatcher = Dispatchers.Default,
) {
    private val scope = CoroutineScope(SupervisorJob() + dispatcher)
    private val mutableView = MutableStateFlow<CallView?>(null)
    val view: StateFlow<CallView?> = mutableView.asStateFlow()

    private val commandLock = Mutex()
    private val signalLock = Mutex()
    private val catchUpLock = Mutex()
    private var current: CallSession? = null
    private var media: CallMedia? = null
    private val ended = mutableSetOf<String>()

    private data class CallSession(
        var id: String? = null,
        val roomId: String,
        val role: CallRole,
        var state: String,
        var owned: Boolean,
        var pending: Boolean = false,
        var mediaStarted: Boolean = false,
        var loopsStarted: Boolean = false,
        var connectedSent: Boolean = false,
        var remote: Boolean = false,
        var connection: CallConnection = CallConnection.UNKNOWN,
        var cursor: Long = 0,
        var senderSeq: Long = 0,
        val ownSignals: MutableSet<Long> = mutableSetOf(),
    )

    fun start(roomId: String) {
        scope.launch {
            commandLock.withLock {
                val existing = current
                if (existing != null && existing.state != "ended") return@withLock
                val call = CallSession(roomId = roomId, role = CALLER, state = "starting", owned = true, pending = true)
                current = call
                publish(call)
                val media = mediaFactory.create()
                this@Calls.media = media
                try {
                    media.prepare()
                    val command = CreateCallRequest(roomId, newId())
                    val created = withRetries { requests.send { createCall(it, command) } }
                    call.pending = false
                    call.id = created.callId
                    call.state = created.state
                    publish(call)
                    refresh(call)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    finish(call, e.message ?: "Не удалось начать звонок")
                }
            }
        }
    }

    fun accept() = command { call ->
        if (call.state != "ringing" || call.role != CALLEE || call.owned) return@command
        call.pending = true
        publish(call)
        val media = mediaFactory.create()
        this@Calls.media = media
        try {
            media.prepare()
            val command = CallCommand(newId())
            val accepted = withRetries { requests.send { acceptCall(it, call.id!!, command) } }
            call.pending = false
            call.owned = true
            apply(call, accepted)
            startMedia(call)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            call.pending = false
            if (e is ApiException && e.status == 404) finish(call)
            else publish(call, error = e.message ?: "Не удалось принять звонок")
        }
    }

    fun reject() = command { call ->
        if (call.state != "ringing" || call.role != CALLEE) return@command
        runCommand(call, "reject") { id, body -> rejectCall(id, call.id!!, body) }
    }

    fun cancel() = command { call ->
        if (call.state != "ringing" || call.role != CALLER) return@command
        runCommand(call, "cancel") { id, body -> cancelCall(id, call.id!!, body) }
    }

    fun end() = command { call ->
        runCommand(call, "end") { id, body -> endCall(id, call.id!!, body) }
    }

    fun dismiss() {
        if (current?.state == "ended") {
            current = null
            mutableView.value = null
        }
    }

    fun onEvent(frame: Frame.CallEvent) {
        scope.launch {
            val call = current
            if (call?.id == frame.callId) {
                if (frame.type == "call.signal") catchUp(call) else refresh(call)
            } else if (frame.callId !in ended) {
                reconcile()
            }
        }
    }

    fun reconcile() {
        scope.launch { reconcileNow() }
    }

    internal suspend fun reconcileNow() {
        try {
            val listed = requests.send { activeCalls(it) }.calls.filter { it.state != "ended" && it.callId !in ended }
            val call = current
            if (call != null && call.state != "ended") {
                val id = call.id ?: return
                val found = listed.find { it.callId == id }
                if (found != null) {
                    apply(call, found)
                    if (call.mediaStarted) catchUp(call)
                    return
                }
                refreshNow(call)
                if (current == call && call.state != "ended") {
                    if (call.mediaStarted) catchUp(call)
                    return
                }
            }
            val found = listed.firstOrNull { it.callerId == me || it.calleeId == me } ?: return
            val role = if (found.callerId == me) CALLER else CALLEE
            val incoming = CallSession(found.callId, found.roomId, role, found.state, owned = false)
            current = incoming
            publish(incoming)
            schedule(incoming)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // The next realtime connection retries reconciliation.
        }
    }

    fun stop() {
        scope.launch {
            val call = current
            try {
                if (call?.id != null && call.state != "ended") {
                    withTimeout(2_000) {
                        runCatching { requests.send { endCall(it, call.id!!, CallCommand(newId())) } }
                    }
                }
            } finally {
                call?.let { finish(it) }
            }
        }
    }

    private fun command(action: suspend (CallSession) -> Unit) {
        scope.launch {
            commandLock.withLock {
                current?.takeIf { it.state != "ended" }?.let { action(it) }
            }
        }
    }

    private suspend fun runCommand(call: CallSession, action: String, request: suspend io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi.(String, CallCommand) -> retrofit2.Response<CallDto>) {
        if (current != call || call.state == "ended") return
        call.pending = true
        publish(call)
        try {
            val command = CallCommand(newId())
            val result = withRetries { requests.send { request(this, it, command) } }
            call.pending = false
            apply(call, result)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            call.pending = false
            if (e is ApiException && e.status == 404) finish(call)
            else publish(call, error = e.message ?: "Команда $action не выполнена")
        }
    }

    private fun refresh(call: CallSession) {
        scope.launch { refreshNow(call) }
    }

    private suspend fun refreshNow(call: CallSession) {
        val id = call.id ?: return
        try {
            apply(call, requests.send { call(it, id) })
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            if (e.status == 404) finish(call)
        } catch (_: Exception) {
        }
    }

    private fun apply(call: CallSession, result: CallDto) {
        if (current != call || call.state == "ended") return
        call.state = result.state
        publish(call)
        if (result.state == "ended") finish(call)
        else {
            if (result.state == "connecting" && call.owned) startMedia(call)
            schedule(call)
        }
    }

    private fun startMedia(call: CallSession) {
        if (call.mediaStarted || call.state !in setOf("connecting", "active")) return
        call.mediaStarted = true
        scope.launch {
            try {
                val turn = runCatching { requests.send { turnCall(it, call.id!!, EmptyCallBody()) } }.getOrNull()
                foreground(true)
                media?.start(call.role, turn?.iceServers ?: emptyList(), mediaListener(call))
                schedule(call)
                refreshTurnBeforeExpiry(call, turn?.ttl ?: 600)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                finish(call, e.message ?: "Не удалось включить звонок")
            }
        }
    }

    private fun mediaListener(call: CallSession) = object : CallMediaListener {
        override suspend fun signal(kind: String, payload: String) = sendSignal(call, kind, payload)
        override fun connected() {
            if (call.connectedSent) return
            call.connectedSent = true
            scope.launch {
                try {
                    val command = CallCommand(newId())
                    apply(call, withRetries { requests.send { connectedCall(it, call.id!!, command) } })
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    call.connectedSent = false
                }
            }
        }
        override suspend fun refreshTurn(): List<IceServerDto> {
            if (current != call || call.state == "ended") return emptyList()
            return requests.send { turnCall(it, call.id!!, EmptyCallBody()) }.iceServers
        }
        override fun remote() {
            call.remote = true
            publish(call)
        }
        override fun connection(value: CallConnection) {
            if (current != call || call.state == "ended") return
            call.connection = value
            publish(call)
        }
        override fun failed() = end()
    }

    private suspend fun sendSignal(call: CallSession, kind: String, payload: String) {
        signalLock.withLock {
            if (current != call || call.state == "ended") return@withLock
            call.senderSeq++
            val seq = call.senderSeq
            var last: Exception? = null
            repeat(3) { attempt ->
                if (attempt > 0) delay(if (attempt == 1) 500 else 1_500)
                try {
                    val result = requests.send { signalCall(it, call.id!!, CallSignalRequest(kind, payload, seq)) }
                    call.ownSignals += result.signalSeq
                    return@withLock
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    last = e
                }
            }
            throw last ?: IllegalStateException("signal failed")
        }
    }

    private suspend fun catchUp(call: CallSession) {
        catchUpLock.withLock {
            if (current != call || call.state == "ended") return@withLock
            try {
                val signals = requests.send { callSignals(it, call.id!!, call.cursor) }.signals.sortedBy { it.signalSeq }
                for (signal in signals) {
                    if (signal.signalSeq <= call.cursor) continue
                    val own = signalLock.withLock { signal.signalSeq in call.ownSignals }
                    if (!own) media?.receive(signal.kind, signal.payload)
                    call.cursor = signal.signalSeq
                }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
            }
        }
    }

    private fun refreshTurnBeforeExpiry(call: CallSession, ttlSeconds: Long) {
        scope.launch {
            val delayMs = (ttlSeconds * 1000 - 60_000).coerceAtLeast(1_000)
            delay(delayMs)
            if (current != call || call.state != "active" || media?.usesRelay() != true) return@launch
            try {
                val turn = requests.send { turnCall(it, call.id!!, EmptyCallBody()) }
                media?.restart(turn.iceServers)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
            }
        }
    }

    private fun schedule(call: CallSession) {
        if (!call.owned || call.loopsStarted || call.state !in setOf("connecting", "active")) return
        call.loopsStarted = true
        scope.launch {
            while (current == call && call.state in setOf("connecting", "active")) {
                delay(15_000)
                if (current != call) break
                runCatching { requests.send { heartbeatCall(it, call.id!!, CallCommand(newId())) } }
                catchUp(call)
            }
        }
        scope.launch {
            while (current == call && call.state in setOf("connecting", "active")) {
                delay(1_000)
                catchUp(call)
            }
        }
    }

    private fun publish(call: CallSession, id: String? = call.id, error: String? = null) {
        if (current != call) return
        mutableView.value = CallView(id, call.roomId, call.state, call.role, call.owned, call.pending, call.remote, call.connection, error)
    }

    private fun finish(call: CallSession, error: String? = null) {
        if (current != call) return
        call.state = "ended"
        call.pending = false
        call.id?.let { ended += it }
        media?.close()
        media = null
        foreground(false)
        publish(call, error = error)
    }

    suspend fun audioBytes(): Pair<Long, Long> = media?.audioBytes() ?: (0L to 0L)

    private suspend fun <T> withRetries(request: suspend () -> T): T {
        var last: ApiException? = null
        repeat(3) { attempt ->
            if (attempt > 0) delay(if (attempt == 1) 500 else 1_500)
            try {
                return request()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException) {
                if (e.status != 0 && e.status != 429) throw e
                last = e
            }
        }
        throw last ?: IllegalStateException("request failed")
    }

    private fun newId(): String = UUID.randomUUID().toString()
}
