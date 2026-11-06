package io.github.alexandrkhromov2005.vaultchat.core.net

import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * The root URL of a Gateway, normalised to `scheme://host[:port]/`. Only
 * HTTPS is accepted, except that debug builds may use plain HTTP to a local
 * stack ([LOCAL_HOSTS]); the network security config enforces the same rule.
 */
class ServerAddress private constructor(val url: HttpUrl) {

    override fun toString(): String = url.toString().removeSuffix("/")

    override fun equals(other: Any?): Boolean = other is ServerAddress && other.url == url

    override fun hashCode(): Int = url.hashCode()

    enum class Problem { EMPTY, MALFORMED, CLEARTEXT, CREDENTIALS, PATH }

    class InvalidException(val problem: Problem) : IllegalArgumentException(problem.name)

    companion object {
        val LOCAL_HOSTS = setOf("10.0.2.2", "localhost", "127.0.0.1")

        /** Parses user input; a missing scheme means HTTPS. */
        fun parse(input: String, allowLocalCleartext: Boolean): ServerAddress {
            val text = input.trim()
            if (text.isEmpty()) throw InvalidException(Problem.EMPTY)
            val withScheme = if ("://" in text) text else "https://$text"
            val url = withScheme.toHttpUrlOrNull() ?: throw InvalidException(Problem.MALFORMED)
            if (url.scheme == "http" && !(allowLocalCleartext && url.host in LOCAL_HOSTS)) {
                throw InvalidException(Problem.CLEARTEXT)
            }
            if (url.username.isNotEmpty() || url.password.isNotEmpty()) {
                throw InvalidException(Problem.CREDENTIALS)
            }
            if (url.encodedPath != "/" || url.query != null || url.fragment != null) {
                throw InvalidException(Problem.PATH)
            }
            return ServerAddress(url)
        }
    }
}
