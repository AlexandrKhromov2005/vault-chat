package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.GatewayJson
import io.github.alexandrkhromov2005.vaultchat.core.net.MessageDto
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonObject

/** A frame from the Gateway's WebSocket (docs/chat/realtime.md). */
sealed interface Frame {
    /** The connection is authenticated until [expiresAt] (server time, as sent). */
    data class Ready(val userId: String, val expiresAt: String?) : Frame

    data class MessageCreated(val roomId: String, val message: MessageDto) : Frame

    data class MemberAdded(val roomId: String, val userId: String) : Frame

    /** `member.removed` and `member.left`: the user is no longer a member. */
    data class MemberGone(val roomId: String, val userId: String) : Frame

    companion object {
        /**
         * Parses a text frame. Unknown types, frames without the fields their
         * type needs and malformed JSON give null and are ignored.
         */
        fun parse(text: String): Frame? {
            val frame = try {
                GatewayJson.parseToJsonElement(text).jsonObject
            } catch (e: SerializationException) {
                return null
            } catch (e: IllegalArgumentException) {
                // Not a JSON object.
                return null
            }
            val type = frame.string("type") ?: return null
            if (type == "ready") return frame.string("user_id")?.let { Ready(it, frame.string("expires_at")) }
            val roomId = frame.string("room_id")?.takeIf(::isUuid) ?: return null
            return when (type) {
                "message.created" -> frame["message"]?.let { message ->
                    try {
                        MessageCreated(roomId, GatewayJson.decodeFromJsonElement<MessageDto>(message))
                    } catch (e: IllegalArgumentException) {
                        // SerializationException is an IllegalArgumentException.
                        null
                    }
                }
                "member.added" -> frame.string("user_id")?.let { MemberAdded(roomId, it) }
                "member.removed", "member.left" -> frame.string("user_id")?.let { MemberGone(roomId, it) }
                else -> null
            }
        }

        private fun JsonObject.string(key: String): String? = (get(key) as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull
    }
}
