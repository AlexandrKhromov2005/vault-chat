package io.github.alexandrkhromov2005.vaultchat

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException

/** A newer release of the app: its version and its page on GitHub. */
data class AppUpdate(val version: String, val page: String)

/**
 * Looks for a newer release of the app on GitHub Releases, at most once a
 * day (docs/android/release.md). Releases of the app are tagged
 * `android-v<version>`. Only the release page is offered, opened in the
 * browser; the app never downloads or installs anything by itself.
 */
class Updates(
    private val http: OkHttpClient,
    private val settings: Settings,
    private val current: String = BuildConfig.VERSION_NAME,
    private val now: () -> Long = System::currentTimeMillis,
    private val releases: String = RELEASES,
) {
    private val found = MutableStateFlow(settings.updateVersion?.let { version -> settings.updatePage?.let { AppUpdate(version, it) } }?.takeIf { newer(it.version, current) })

    /** The newer release, if one was found. */
    val update: StateFlow<AppUpdate?> = found

    /** Checks GitHub if the last check is a day old; a failure waits for the next day. */
    suspend fun checkIfDue() {
        if (now() - settings.updateCheckedAt < DAY_MS) return
        settings.updateCheckedAt = now()
        val latest = try {
            withContext(Dispatchers.IO) { fetch() }
        } catch (e: IOException) {
            return
        } catch (e: IllegalArgumentException) {
            // Not the JSON GitHub describes.
            return
        }
        val update = latest?.takeIf { newer(it.version, current) }
        settings.updateVersion = update?.version
        settings.updatePage = update?.page
        found.value = update
    }

    private fun fetch(): AppUpdate? {
        val request = Request.Builder().url(releases).header("Accept", "application/vnd.github+json").build()
        http.newCall(request).execute().use { response ->
            if (!response.isSuccessful) return null
            val source = response.body.source()
            if (source.request(MAX_BYTES + 1)) return null
            val list = json.decodeFromString<List<Release>>(source.readUtf8())
            return list.asSequence()
                .filter { !it.draft && !it.prerelease && it.tag.startsWith(TAG_PREFIX) && it.page.startsWith(PAGE_PREFIX) }
                .mapNotNull { release -> release.tag.removePrefix(TAG_PREFIX).takeIf(::isVersion)?.let { AppUpdate(it, release.page) } }
                .maxWithOrNull { a, b -> compare(a.version, b.version) }
        }
    }

    @Serializable
    private data class Release(
        @SerialName("tag_name") val tag: String,
        @SerialName("html_url") val page: String,
        val draft: Boolean = false,
        val prerelease: Boolean = false,
    )

    companion object {
        private const val REPOSITORY = "AlexandrKhromov2005/vault-chat"
        private const val RELEASES = "https://api.github.com/repos/$REPOSITORY/releases?per_page=30"

        // Only a page of this repository is ever opened.
        private const val PAGE_PREFIX = "https://github.com/$REPOSITORY/releases/"
        const val TAG_PREFIX = "android-v"

        private const val DAY_MS = 24 * 60 * 60 * 1000L
        private const val MAX_BYTES = 1024L * 1024

        private val json = Json { ignoreUnknownKeys = true }

        private fun isVersion(value: String) = Regex("[0-9]{1,6}(\\.[0-9]{1,6}){0,3}").matches(value)

        /** Whether version [candidate] is newer than [current], compared number by number. */
        fun newer(candidate: String, current: String): Boolean = isVersion(candidate) && compare(candidate, current) > 0

        private fun compare(a: String, b: String): Int {
            val x = a.split('.').map { it.toIntOrNull() ?: 0 }
            val y = b.split('.').map { it.toIntOrNull() ?: 0 }
            for (i in 0 until maxOf(x.size, y.size)) {
                val diff = x.getOrElse(i) { 0 }.compareTo(y.getOrElse(i) { 0 })
                if (diff != 0) return diff
            }
            return 0
        }
    }
}
