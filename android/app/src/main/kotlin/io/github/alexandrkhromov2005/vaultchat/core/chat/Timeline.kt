package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.chat.db.MessageEntity
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.TimelineEntity

/**
 * How pages and live messages change the stored block of a room.
 *
 * Messages are numbered per room 1, 2, 3, ... without gaps, so the block is
 * kept contiguous: it holds every message from `oldest` to `newest` and no
 * other. Every function takes the current block (null when the room has not
 * been loaded) and returns what to store, or null when nothing changes. The
 * pages are already checked (see Pages.kt).
 */
object Timeline {

    /** What to write: the new block, the messages to insert, and whether to drop the stored ones first. */
    data class Update(val timeline: TimelineEntity, val insert: List<MessageEntity>, val replace: Boolean = false)

    /** The result of extending a block; [gap] means messages before some of them are missing. */
    data class Extension(val update: Update?, val gap: Boolean)

    /**
     * Applies the latest history page, newest first. A page that reaches the
     * block extends it; one that does not replaces it, because the messages
     * between them are unknown. History that is present when a room is
     * first loaded counts as read.
     */
    fun latest(roomId: String, block: TimelineEntity?, page: List<MessageEntity>): Update? {
        if (page.isEmpty()) {
            // An empty room. A loaded block cannot become empty: messages are
            // never deleted, so such an answer is ignored.
            return if (block == null) Update(TimelineEntity(roomId, newest = 0, oldest = 1, lastRead = 0), emptyList()) else null
        }
        val top = page.first().sequence
        val bottom = page.last().sequence
        return when {
            block == null ->
                Update(TimelineEntity(roomId, newest = top, oldest = bottom, lastRead = top), page, replace = true)
            top <= block.newest -> null
            bottom <= block.newest + 1 ->
                Update(block.copy(newest = top), page.filter { it.sequence > block.newest })
            else ->
                Update(block.copy(newest = top, oldest = bottom), page, replace = true)
        }
    }

    /**
     * Applies messages that follow the block, oldest first: a catch-up page or
     * a live message. Known sequences are duplicates; the first message that
     * does not directly follow the block ends the extension as a gap, which
     * the caller fills by catching up.
     */
    fun extend(block: TimelineEntity?, messages: List<MessageEntity>): Extension {
        if (block == null) return Extension(null, gap = messages.isNotEmpty())
        var newest = block.newest
        val insert = mutableListOf<MessageEntity>()
        for (message in messages) {
            when {
                message.sequence <= newest -> continue
                message.sequence == newest + 1 -> {
                    insert += message
                    newest = message.sequence
                }
                else -> return Extension(insert.toUpdate(block, newest), gap = true)
            }
        }
        return Extension(insert.toUpdate(block, newest), gap = false)
    }

    private fun List<MessageEntity>.toUpdate(block: TimelineEntity, newest: Long): Update? =
        if (isEmpty()) null else Update(block.copy(newest = newest), this)

    /**
     * Applies an older history page, newest first, read with
     * `cursor = oldest`. It is stored only if it directly precedes the block;
     * otherwise the block changed meanwhile and the page is dropped.
     */
    fun older(block: TimelineEntity?, page: List<MessageEntity>): Update? {
        if (block == null || page.isEmpty()) return null
        if (page.first().sequence != block.oldest - 1) return null
        return Update(block.copy(oldest = page.last().sequence), page)
    }
}
