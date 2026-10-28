package io.github.alexandrkhromov2005.vaultchat.core.session

import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import javax.crypto.AEADBadTagException

/**
 * What survives a restart of the app. The access token is not stored: it is
 * kept in memory and obtained again with the refresh token.
 *
 * [refreshPendingSince] is set, on the device clock, before a refresh is
 * sent and cleared once its answer is stored. While it is set the server may
 * already have retired [refreshToken]; see SessionManager.
 */
@Serializable
data class StoredSession(
    val server: String,
    val userId: String,
    val username: String,
    val refreshToken: String,
    val refreshPendingSince: Long? = null,
)

interface SessionStore {
    /**
     * The stored session, or null when there is none or it can never be read.
     * Throws when it cannot be read now but may be later.
     */
    suspend fun load(): StoredSession?

    suspend fun save(session: StoredSession)

    suspend fun clear()
}

/** Authenticated encryption of the stored session, see [KeystoreSessionCipher]. */
interface SessionCipher {
    fun encrypt(plaintext: ByteArray, associatedData: ByteArray): ByteArray

    /**
     * Throws [AEADBadTagException] or [UnreadableSessionException] for
     * ciphertext that can never be decrypted; anything else is taken as a
     * passing failure.
     */
    fun decrypt(ciphertext: ByteArray, associatedData: ByteArray): ByteArray

    /** Forgets the key, so that ciphertext left anywhere can no longer be read. */
    fun destroyKey()
}

/** Stored ciphertext that can never be decrypted, e.g. because its key is gone. */
class UnreadableSessionException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * Keeps the session in one encrypted file. A file that can never be read —
 * tampered with, truncated, or encrypted with a key the Keystore has lost —
 * is deleted together with the key, and the user signs in again. A passing
 * failure of the Keystore or of the disk is reported and the file is kept.
 */
class EncryptedFileSessionStore(
    private val file: File,
    private val cipher: SessionCipher,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) : SessionStore {

    override suspend fun load(): StoredSession? = withContext(io) {
        if (!file.exists()) return@withContext null
        try {
            if (file.length() > MAX_FILE_BYTES) throw UnreadableSessionException("session file too large")
            val plaintext = cipher.decrypt(file.readBytes(), ASSOCIATED_DATA)
            json.decodeFromString(StoredSession.serializer(), plaintext.decodeToString())
        } catch (e: Exception) {
            // SerializationException is an IllegalArgumentException.
            if (e !is AEADBadTagException && e !is UnreadableSessionException && e !is IllegalArgumentException) throw e
            discard()
            null
        }
    }

    override suspend fun save(session: StoredSession) = withContext(io) {
        val plaintext = json.encodeToString(StoredSession.serializer(), session).encodeToByteArray()
        val ciphertext = try {
            cipher.encrypt(plaintext, ASSOCIATED_DATA)
        } catch (e: Exception) {
            // A Keystore key that can no longer encrypt would block every sign-in;
            // the file is overwritten anyway, so start over with a new key once.
            cipher.destroyKey()
            cipher.encrypt(plaintext, ASSOCIATED_DATA)
        }
        // Write a sibling file, flush it to the device and rename it over the
        // old one, so that a crash or power loss leaves either the previous
        // session or the new one, never an empty or mixed file.
        val temporary = File(file.parentFile, file.name + ".tmp")
        FileOutputStream(temporary).use {
            it.write(ciphertext)
            it.fd.sync()
        }
        Files.move(temporary.toPath(), file.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        Unit
    }

    override suspend fun clear() = withContext(io) { discard() }

    private fun discard() {
        file.delete()
        File(file.parentFile, file.name + ".tmp").delete()
        // Without the file the key protects nothing; a failure to delete it
        // must not keep the user signed in.
        runCatching { cipher.destroyKey() }
    }

    private companion object {
        const val MAX_FILE_BYTES = 64L * 1024

        // Binds the ciphertext to its purpose and format version.
        val ASSOCIATED_DATA = "vault-chat session v1".encodeToByteArray()

        val json = Json { ignoreUnknownKeys = true }
    }
}
