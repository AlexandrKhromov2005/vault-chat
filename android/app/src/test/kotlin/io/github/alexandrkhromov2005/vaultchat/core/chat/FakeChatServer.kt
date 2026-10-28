package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.EmailAcknowledgement
import io.github.alexandrkhromov2005.vaultchat.core.net.EmailVerificationRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayApi
import io.github.alexandrkhromov2005.vaultchat.core.net.LoginRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.MessageDto
import io.github.alexandrkhromov2005.vaultchat.core.net.MessagePage
import io.github.alexandrkhromov2005.vaultchat.core.net.PublicProfile
import io.github.alexandrkhromov2005.vaultchat.core.net.RefreshTokenRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.RegisterRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.Registration
import io.github.alexandrkhromov2005.vaultchat.core.net.RequestProgress
import io.github.alexandrkhromov2005.vaultchat.core.net.RoomDto
import io.github.alexandrkhromov2005.vaultchat.core.net.RoomPage
import io.github.alexandrkhromov2005.vaultchat.core.net.SendMessageRequest
import io.github.alexandrkhromov2005.vaultchat.core.net.TokenPair
import io.github.alexandrkhromov2005.vaultchat.core.net.User
import io.github.alexandrkhromov2005.vaultchat.core.net.call
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.Response
import java.time.Instant
import java.util.Collections

/**
 * A model of the Gateway's chat API for one user, with the paging rules of
 * docs/api/gateway.yaml: rooms most recently active first, history newest
 * first before a cursor, catch-up oldest first after a position.
 */
class FakeChatServer(val me: String) : GatewayApi {
    /** Rooms of [me], most recently active first. */
    val rooms: MutableList<RoomDto> = Collections.synchronizedList(mutableListOf())
    val messages = mutableMapOf<String, MutableList<MessageDto>>()
    val profiles = mutableMapOf<String, String>()
    private val log: MutableList<String> = Collections.synchronizedList(mutableListOf())

    /** The calls so far, in order. */
    fun calls(): List<String> = synchronized(log) { log.toList() }

    /** Answers a call with this HTTP error status instead, when it returns one. */
    @Volatile
    var intercept: (String) -> Int? = { null }

    /**
     * Runs after a send has been stored (or found as a repeat), before the
     * answer; throwing an IOException here loses the answer.
     */
    @Volatile
    var afterSend: suspend (MessageDto) -> Unit = {}

    /** What a send answers instead of the stored message. */
    @Volatile
    var reply: (MessageDto) -> MessageDto = { it }

    /** Runs after a room page has been prepared, before it is returned. */
    @Volatile
    var afterRoomPage: (index: Int) -> Unit = {}

    private var clock = Instant.parse("2026-10-27T12:00:00Z")
    private var roomPages = 0

    private fun tick(): String {
        clock = clock.plusSeconds(1)
        return clock.toString()
    }

    fun id(kind: Int, n: Int) = "00000000-0000-4000-8%03d-%012d".format(kind, n)

    @Synchronized
    fun addChannel(n: Int, name: String = "room $n"): String {
        val roomId = id(1, n)
        rooms.add(0, RoomDto(roomId, "channel", name = name, ownerId = me, createdAt = tick()))
        messages[roomId] = mutableListOf()
        return roomId
    }

    @Synchronized
    fun addDialog(n: Int, peer: String, peerName: String): String {
        val roomId = id(1, n)
        profiles[peer] = peerName
        rooms.add(0, RoomDto(roomId, "direct", createdAt = tick(), participantIds = listOf(me, peer).sorted()))
        messages[roomId] = mutableListOf()
        return roomId
    }

    @Synchronized
    fun removeRoom(roomId: String) {
        rooms.removeAll { it.roomId == roomId }
    }

    /** Stores a message and moves its room to the top. */
    @Synchronized
    fun post(roomId: String, sender: String = me, text: String = "text"): MessageDto {
        val n = messages.getValue(roomId).size + 1
        return store(roomId, sender, id(3, n + roomId.hashCode().and(0xffff) * 1000), "$text $n")
    }

    /** Stores a message with exactly this key and text, and moves its room to the top. */
    @Synchronized
    fun store(roomId: String, sender: String, clientMessageId: String, text: String): MessageDto {
        val list = messages.getValue(roomId)
        val message = MessageDto(
            messageId = id(2, list.size + 1 + roomId.hashCode().and(0xffff) * 1000),
            roomId = roomId,
            senderId = sender,
            clientMessageId = clientMessageId,
            text = text,
            sequence = list.size + 1L,
            createdAt = tick(),
        )
        list += message
        val index = rooms.indexOfFirst { it.roomId == roomId }
        if (index >= 0) rooms.add(0, rooms.removeAt(index).copy(lastMessageAt = message.createdAt))
        return message
    }

    fun postMany(roomId: String, count: Int, sender: String = me) = repeat(count) { post(roomId, sender) }

    private fun <T> ok(body: T): Response<T> = Response.success(body)

    private fun <T> error(code: Int): Response<T> =
        Response.error(
            """{"error":{"code":"error","message":"HTTP $code"}}""".toResponseBody("application/json".toMediaType()),
            okhttp3.Response.Builder()
                .request(Request.Builder().url("http://127.0.0.1/").build())
                .protocol(Protocol.HTTP_1_1)
                .code(code)
                .message("")
                .header("Retry-After", "1")
                .build(),
        )

    override suspend fun rooms(authorization: String, cursor: String?, limit: Int): Response<RoomPage> {
        log += "rooms $cursor"
        intercept("rooms")?.let { return error(it) }
        val page = synchronized(this) {
            val start = cursor?.toInt() ?: 0
            val snapshot = rooms.toList()
            val end = minOf(snapshot.size, start + limit)
            RoomPage(snapshot.subList(start, end), if (end < snapshot.size) end.toString() else "")
        }
        afterRoomPage(roomPages++)
        return ok(page)
    }

    override suspend fun room(authorization: String, roomId: String): Response<RoomDto> {
        log += "room $roomId"
        intercept("room $roomId")?.let { return error(it) }
        return synchronized(this) { rooms.firstOrNull { it.roomId == roomId } }?.let(::ok) ?: error(404)
    }

    override suspend fun messages(authorization: String, roomId: String, cursor: String?, after: String?, limit: Int): Response<MessagePage> {
        log += "messages $roomId cursor=$cursor after=$after"
        intercept("messages $roomId")?.let { return error(it) }
        return synchronized(this) {
            if (rooms.none { it.roomId == roomId }) return error(404)
            val list = messages.getValue(roomId)
            if (after != null) {
                val page = list.filter { it.sequence > after.toLong() }.take(limit)
                val more = page.isNotEmpty() && page.last().sequence < list.size
                ok(MessagePage(page, if (more) page.last().sequence.toString() else ""))
            } else {
                val before = cursor?.toLong() ?: Long.MAX_VALUE
                val page = list.filter { it.sequence < before }.reversed().take(limit)
                val more = page.isNotEmpty() && page.last().sequence > 1
                ok(MessagePage(page, if (more) page.last().sequence.toString() else ""))
            }
        }
    }

    /** Stores by (room, sender, key) like Chat: a repeat returns the original, another text is a conflict. */
    override suspend fun sendMessage(authorization: String, roomId: String, body: SendMessageRequest): Response<MessageDto> {
        log += "send $roomId ${body.clientMessageId}"
        intercept("send $roomId")?.let { return error(it) }
        val message = synchronized(this) {
            if (rooms.none { it.roomId == roomId }) return error(404)
            val existing = messages.getValue(roomId).firstOrNull { it.senderId == me && it.clientMessageId == body.clientMessageId }
            when {
                existing == null -> store(roomId, me, body.clientMessageId, body.text)
                existing.text != body.text -> return error(409)
                else -> existing
            }
        }
        afterSend(message)
        return ok(reply(message))
    }

    /** The messages of [me] in a room, oldest first. */
    @Synchronized
    fun sent(roomId: String): List<MessageDto> = messages.getValue(roomId).filter { it.senderId == me }

    override suspend fun user(authorization: String, userId: String): Response<PublicProfile> {
        log += "user $userId"
        intercept("user $userId")?.let { return error(it) }
        return synchronized(this) { profiles[userId] }?.let { ok(PublicProfile(userId, it)) } ?: error(404)
    }

    /** Requests as the client sends them, through the same error mapping. */
    val requests = object : ChatRequests {
        override suspend fun <T : Any> send(request: suspend GatewayApi.(authorization: String) -> Response<T>): T =
            call { this@FakeChatServer.request("Bearer test") }.body
    }

    override suspend fun register(body: RegisterRequest): Response<Registration> = throw UnsupportedOperationException()

    override suspend fun login(body: LoginRequest): Response<TokenPair> = throw UnsupportedOperationException()

    override suspend fun requestEmailVerification(body: EmailVerificationRequest): Response<EmailAcknowledgement> =
        throw UnsupportedOperationException()

    override suspend fun refresh(body: RefreshTokenRequest, progress: RequestProgress): Response<TokenPair> =
        throw UnsupportedOperationException()

    override suspend fun logout(body: RefreshTokenRequest): Response<Unit> = throw UnsupportedOperationException()

    override suspend fun me(authorization: String): Response<User> = throw UnsupportedOperationException()
}
