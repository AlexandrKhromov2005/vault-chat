package io.github.alexandrkhromov2005.vaultchat.core.session

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class EncryptedFileSessionStoreTest {
    @get:Rule
    val folder = TemporaryFolder()

    private val session = StoredSession("https://chat.example.org", "user-1", "alice", "refresh-secret-0123456789")

    @Test
    fun roundTripsWithoutPlaintextOnDisk() = runBlocking {
        val (store, file, _) = newStore()
        store.save(session)
        assertEquals(session, store.load())
        assertFalse(String(file.readBytes(), Charsets.ISO_8859_1).contains("refresh-secret"))
        assertFalse(File(file.parentFile, file.name + ".tmp").exists())
    }

    @Test
    fun aTamperedFileIsDiscardedWithItsKey() = runBlocking {
        val (store, file, cipher) = newStore()
        store.save(session)
        val bytes = file.readBytes()
        bytes[bytes.size - 1] = (bytes[bytes.size - 1].toInt() xor 1).toByte()
        file.writeBytes(bytes)

        assertNull(store.load())
        assertFalse(file.exists())
        assertTrue(cipher.destroyed)
    }

    @Test
    fun aTruncatedOrOversizedFileIsDiscarded() = runBlocking {
        val (store, file, _) = newStore()
        file.writeBytes(ByteArray(3))
        assertNull(store.load())
        assertFalse(file.exists())

        file.writeBytes(ByteArray(65 * 1024))
        assertNull(store.load())
        assertFalse(file.exists())
    }

    @Test
    fun aFileFromAnotherKeyIsDiscarded() = runBlocking {
        val (first, file, _) = newStore()
        first.save(session)
        // The Keystore lost the key, e.g. after a restore onto another device.
        val second = EncryptedFileSessionStore(file, JvmCipher())
        assertNull(second.load())
        assertFalse(file.exists())
    }

    @Test
    fun aTransientKeystoreFailureKeepsTheFile() = runBlocking {
        val (store, file, cipher) = newStore()
        store.save(session)
        cipher.failure = java.security.KeyStoreException("keystore busy")

        try {
            store.load()
            fail("load succeeded")
        } catch (e: Exception) {
            // Reported to the caller, which may try again.
        }
        assertTrue(file.exists())
        assertFalse(cipher.destroyed)

        cipher.failure = null
        assertEquals(session, store.load())
    }

    @Test
    fun aKeyThatFailsToEncryptIsReplaced() = runBlocking {
        val (store, _, cipher) = newStore()
        cipher.failEncryptions = 1

        store.save(session)

        assertTrue(cipher.destroyed)
        assertEquals(session, store.load())
    }

    @Test
    fun clearRemovesTheFile() = runBlocking {
        val (store, file, cipher) = newStore()
        store.save(session)
        store.clear()
        assertFalse(file.exists())
        assertTrue(cipher.destroyed)
        assertNull(store.load())
    }

    private fun newStore(): Triple<EncryptedFileSessionStore, File, JvmCipher> {
        val file = File(folder.newFolder(), "session.bin")
        val cipher = JvmCipher()
        return Triple(EncryptedFileSessionStore(file, cipher), file, cipher)
    }
}

/** AES-GCM in the JVM's provider, standing in for the Android Keystore. */
private class JvmCipher : SessionCipher {
    private var key: SecretKey = newKey()
    var destroyed = false
    var failure: Exception? = null
    var failEncryptions = 0

    private fun newKey(): SecretKey = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()

    override fun encrypt(plaintext: ByteArray, associatedData: ByteArray): ByteArray {
        if (failEncryptions > 0) {
            failEncryptions--
            throw java.security.ProviderException("keystore operation failed")
        }
        val iv = ByteArray(12).also { SecureRandom().nextBytes(it) }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key, GCMParameterSpec(128, iv))
        cipher.updateAAD(associatedData)
        return iv + cipher.doFinal(plaintext)
    }

    override fun decrypt(ciphertext: ByteArray, associatedData: ByteArray): ByteArray {
        failure?.let { throw it }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, ciphertext, 0, 12))
        cipher.updateAAD(associatedData)
        return cipher.doFinal(ciphertext, 12, ciphertext.size - 12)
    }

    override fun destroyKey() {
        destroyed = true
        key = newKey()
    }
}
