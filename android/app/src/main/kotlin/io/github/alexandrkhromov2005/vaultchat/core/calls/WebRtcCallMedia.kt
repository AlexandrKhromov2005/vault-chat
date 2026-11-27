package io.github.alexandrkhromov2005.vaultchat.core.calls

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat
import io.github.alexandrkhromov2005.vaultchat.BuildConfig
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayJson
import io.github.alexandrkhromov2005.vaultchat.core.net.IceServerDto
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.Serializable
import org.webrtc.AudioSource
import org.webrtc.AudioTrack
import org.webrtc.DataChannel
import org.webrtc.IceCandidate
import org.webrtc.MediaConstraints
import org.webrtc.MediaStream
import org.webrtc.PeerConnection
import org.webrtc.PeerConnectionFactory
import org.webrtc.RTCStatsCollectorCallback
import org.webrtc.RTCStatsReport
import org.webrtc.RtpReceiver
import org.webrtc.SdpObserver
import org.webrtc.SessionDescription
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** Android WebRTC audio for [Calls]; no signaling content is logged. */
class WebRtcCallMedia(
    private val context: Context,
) : CallMedia {
    private val scope = CoroutineScope(
        SupervisorJob() + Dispatchers.Default + CoroutineExceptionHandler { _, _ -> listener?.failed() },
    )
    private val operationLock = Mutex()
    private var factory: PeerConnectionFactory? = null
    private var source: AudioSource? = null
    private var track: AudioTrack? = null
    private var peer: PeerConnection? = null
    private var role = CallRole.CALLER
    private var listener: CallMediaListener? = null
    private var configuration = PeerConnection.RTCConfiguration(emptyList())
    private var everConnected = false
    private var recovering = false
    private var restarts = 0
    @Volatile private var localCandidates = 0
    private var remoteCandidates = 0
    private var rejectedCandidates = 0
    private var invalidCandidates = 0
    private var droppedSignals = 0

    override suspend fun prepare() {
        require(ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
            "Нет доступа к микрофону"
        }
        if (factory != null) return
        PeerConnectionFactory.initialize(PeerConnectionFactory.InitializationOptions.builder(context).createInitializationOptions())
        factory = PeerConnectionFactory.builder().createPeerConnectionFactory()
        source = factory!!.createAudioSource(MediaConstraints())
        track = factory!!.createAudioTrack("calls-audio", source)
        track!!.setEnabled(true)
    }

    override suspend fun start(role: CallRole, iceServers: List<IceServerDto>, listener: CallMediaListener): Unit = operationLock.withLock {
        prepare()
        this.role = role
        this.listener = listener
        configuration = configuration(iceServers)
        peer = factory!!.createPeerConnection(configuration, observer)
        peer!!.addTrack(track, listOf("calls"))
        if (role == CallRole.CALLER) offer(restart = false)
    }

    override suspend fun receive(kind: String, payload: String): Unit = operationLock.withLock {
        val peer = peer ?: run {
            droppedSignals++
            return@withLock
        }
        when (kind) {
            "offer" -> {
                peer.setRemoteDescriptionSafe(SessionDescription(SessionDescription.Type.OFFER, payload))
                drainCandidates()
                if (everConnected) peer.setConfiguration(configuration)
                val answer = peer.createAnswerSafe()
                listener?.signal("answer", answer.description)
                peer.setLocalDescriptionSafe(answer)
            }
            "answer" -> {
                peer.setRemoteDescriptionSafe(SessionDescription(SessionDescription.Type.ANSWER, payload))
                drainCandidates()
            }
            "candidate" -> {
                val candidate = decodeCandidate(payload)
                if (candidate == null) {
                    invalidCandidates++
                } else {
                    remoteCandidates++
                    if (peer.remoteDescription == null) pending += candidate
                    else if (!peer.addIceCandidate(candidate)) rejectedCandidates++
                }
            }
            "end-of-candidates" -> Unit
            else -> Unit
        }
    }

    override suspend fun usesRelay(): Boolean = operationLock.withLock {
        val peer = peer ?: return@withLock false
        val report = peer.stats()
        val entries = report.statsMap
        val pair = entries.values.firstOrNull { it.type == "transport" }
            ?.let { it.members["selectedCandidatePairId"]?.toString()?.let(entries::get) }
            ?: entries.values.firstOrNull { it.type == "candidate-pair" && it.members["selected"] == true }
            ?: return@withLock false
        return@withLock listOf("localCandidateId", "remoteCandidateId").any { key ->
            pair.members[key]?.toString()?.let(entries::get)?.members?.get("candidateType") == "relay"
        }
    }

    override suspend fun audioBytes(): Pair<Long, Long> = operationLock.withLock {
        val report = peer?.stats() ?: return@withLock (0L to 0L)
        var sent = 0L
        var received = 0L
        report.statsMap.values.forEach { entry ->
            if (entry.type == "outbound-rtp" && entry.members["kind"] == "audio") {
                sent += (entry.members["bytesSent"] as? Number)?.toLong() ?: 0L
            }
            if (entry.type == "inbound-rtp" && entry.members["kind"] == "audio") {
                received += (entry.members["bytesReceived"] as? Number)?.toLong() ?: 0L
            }
        }
        return@withLock (sent to received)
    }

    internal suspend fun rtcDiagnostics(): String {
        if (!operationLock.tryLock()) return "media operation in progress"
        return try {
            val peer = peer ?: return "peer=null"
            "ice=${peer.iceConnectionState()}, gathering=${peer.iceGatheringState()}, signaling=${peer.signalingState()}, " +
                "candidates=$localCandidates/$remoteCandidates, pending=${pending.size}, rejected=$rejectedCandidates, " +
                "invalid=$invalidCandidates, droppedSignals=$droppedSignals"
        } finally {
            operationLock.unlock()
        }
    }

    override suspend fun restart(iceServers: List<IceServerDto>): Unit = operationLock.withLock {
        configuration = configuration(iceServers)
        peer?.setConfiguration(configuration)
        if (role == CallRole.CALLER) offer(restart = true)
    }

    override fun close() = runBlocking {
        operationLock.withLock {
            recovering = false
            scope.cancel()
            peer?.close()
            peer?.dispose()
            peer = null
            track?.setEnabled(false)
            track?.dispose()
            track = null
            source?.dispose()
            source = null
            factory?.dispose()
            factory = null
            pending.clear()
            listener = null
        }
    }

    private val observer = object : PeerConnection.Observer {
        override fun onSignalingChange(state: PeerConnection.SignalingState?) {}
        override fun onIceConnectionReceivingChange(receiving: Boolean) {}
        override fun onIceGatheringChange(state: PeerConnection.IceGatheringState?) {}
        override fun onIceCandidatesRemoved(candidates: Array<out IceCandidate>?) {}
        override fun onDataChannel(channel: DataChannel?) {}
        override fun onRenegotiationNeeded() {}
        override fun onAddTrack(receiver: RtpReceiver?, mediaStreams: Array<out MediaStream>?) {}
        override fun onRemoveStream(stream: MediaStream?) {}
        override fun onAddStream(stream: MediaStream?) {
            if (stream?.audioTracks?.isNotEmpty() == true) listener?.remote()
        }
        override fun onIceCandidate(candidate: IceCandidate) {
            localCandidates++
            scope.launch { listener?.signal("candidate", GatewayJson.encodeToString(CandidatePayload.serializer(), candidate.payload())) }
        }
        override fun onIceConnectionChange(state: PeerConnection.IceConnectionState?) {
            when (state) {
                PeerConnection.IceConnectionState.CONNECTED, PeerConnection.IceConnectionState.COMPLETED -> {
                    everConnected = true
                    recovering = false
                    restarts = 0
                    listener?.connected()
                    scope.launch {
                        val relay = runCatching { usesRelay() }.getOrNull() ?: return@launch
                        listener?.connection(if (relay) CallConnection.RELAY else CallConnection.DIRECT)
                    }
                }
                PeerConnection.IceConnectionState.FAILED -> recover(failed = true)
                PeerConnection.IceConnectionState.DISCONNECTED -> recover(failed = false)
                else -> Unit
            }
        }
    }

    private val pending = mutableListOf<IceCandidate>()

    private fun drainCandidates() {
        val candidates = pending.toList()
        pending.clear()
        candidates.forEach { candidate ->
            if (!peer!!.addIceCandidate(candidate)) rejectedCandidates++
        }
    }

    private fun recover(failed: Boolean) {
        if (recovering) return
        if (!everConnected) {
            if (failed) listener?.failed()
            return
        }
        recovering = true
        listener?.connection(CallConnection.RECOVERING)
        scope.launch {
            delay(if (failed) 0 else 2_000)
            while (recovering && restarts < 3) {
                restarts++
                try {
                    val servers = listener?.let { it.refreshTurn() } ?: emptyList()
                    if (role == CallRole.CALLER) restart(servers) else {
                        configuration = configuration(servers)
                        peer?.setConfiguration(configuration)
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                }
                delay(8_000)
            }
            if (recovering) listener?.failed()
        }
    }

    private suspend fun offer(restart: Boolean) {
        val peer = peer ?: return
        if (peer.signalingState() != PeerConnection.SignalingState.STABLE) return
        val constraints = MediaConstraints().apply {
            if (restart) mandatory += MediaConstraints.KeyValuePair("IceRestart", "true")
        }
        val offer = peer.createOfferSafe(constraints)
        listener?.signal("offer", offer.description)
        peer.setLocalDescriptionSafe(offer)
    }

    private fun configuration(servers: List<IceServerDto>): PeerConnection.RTCConfiguration =
        PeerConnection.RTCConfiguration(servers.map { server ->
            PeerConnection.IceServer.builder(server.urls).setUsername(server.username).setPassword(server.credential).createIceServer()
        }).apply {
            bundlePolicy = PeerConnection.BundlePolicy.MAXBUNDLE
            rtcpMuxPolicy = PeerConnection.RtcpMuxPolicy.REQUIRE
            iceTransportsType = if (BuildConfig.CALLS_FORCE_RELAY) {
                PeerConnection.IceTransportsType.RELAY
            } else {
                PeerConnection.IceTransportsType.ALL
            }
        }

    private fun IceCandidate.payload() = CandidatePayload(sdp, sdpMid, sdpMLineIndex)

    private fun decodeCandidate(payload: String): IceCandidate? = try {
        val decoded = GatewayJson.decodeFromString(CandidatePayload.serializer(), payload)
        if (decoded.candidate.isEmpty() || (decoded.sdpMid == null && decoded.sdpMLineIndex == null)) null
        else IceCandidate(decoded.sdpMid, decoded.sdpMLineIndex ?: 0, decoded.candidate)
    } catch (_: Exception) {
        null
    }

}

@Serializable
private data class CandidatePayload(
    val candidate: String,
    val sdpMid: String? = null,
    val sdpMLineIndex: Int? = null,
    val usernameFragment: String? = null,
)

private suspend fun PeerConnection.createOfferSafe(constraints: MediaConstraints = MediaConstraints()): SessionDescription =
    suspendCancellableCoroutine { continuation ->
        createOffer(object : SdpObserver {
            override fun onCreateSuccess(description: SessionDescription) = continuation.resume(description)
            override fun onCreateFailure(error: String?) = continuation.resumeWithException(IllegalStateException(error))
            override fun onSetSuccess() = Unit
            override fun onSetFailure(error: String?) = Unit
        }, constraints)
    }

private suspend fun PeerConnection.createAnswerSafe(): SessionDescription =
    suspendCancellableCoroutine { continuation ->
        createAnswer(object : SdpObserver {
            override fun onCreateSuccess(description: SessionDescription) = continuation.resume(description)
            override fun onCreateFailure(error: String?) = continuation.resumeWithException(IllegalStateException(error))
            override fun onSetSuccess() = Unit
            override fun onSetFailure(error: String?) = Unit
        }, MediaConstraints())
    }

private suspend fun PeerConnection.setLocalDescriptionSafe(description: SessionDescription) =
    setDescription(description, local = true)

private suspend fun PeerConnection.setRemoteDescriptionSafe(description: SessionDescription) =
    setDescription(description, local = false)

private suspend fun PeerConnection.setDescription(description: SessionDescription, local: Boolean) =
    suspendCancellableCoroutine<Unit> { continuation ->
        val observer = object : SdpObserver {
            override fun onCreateSuccess(description: SessionDescription?) = Unit
            override fun onCreateFailure(error: String?) = Unit
            override fun onSetSuccess() = continuation.resume(Unit)
            override fun onSetFailure(error: String?) = continuation.resumeWithException(IllegalStateException(error))
        }
        if (local) setLocalDescription(observer, description) else setRemoteDescription(observer, description)
    }

private suspend fun PeerConnection.stats(): RTCStatsReport = withTimeout(30_000) {
    suspendCancellableCoroutine { continuation ->
        getStats(RTCStatsCollectorCallback { continuation.resume(it) })
    }
}
