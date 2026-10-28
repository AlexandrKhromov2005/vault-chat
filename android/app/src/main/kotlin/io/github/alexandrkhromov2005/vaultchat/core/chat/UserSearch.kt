package io.github.alexandrkhromov2005.vaultchat.core.chat

import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import io.github.alexandrkhromov2005.vaultchat.core.net.PublicProfile
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * The search field of the new-chat and add-member screens. A query is sent
 * [delayMs] after the last change, only if the server would accept it, and a
 * newer query cancels the request of an older one, so only the newest answer
 * is ever shown (docs/android/rooms.md).
 */
class UserSearch(
    scope: CoroutineScope,
    private val search: suspend (String) -> List<PublicProfile>,
    private val delayMs: Long = DELAY_MS,
) {
    data class State(
        /** The query as it is sent. */
        val query: String = "",
        /** Why [query] is not searched for; null when it is. */
        val problem: UserQuery.Problem? = UserQuery.Problem.TOO_SHORT,
        val searching: Boolean = false,
        /** Users found for [query]; those of the previous query while a search runs. */
        val users: List<PublicProfile> = emptyList(),
        val error: ApiException? = null,
    )

    private val input = MutableStateFlow("")
    private val attempts = MutableStateFlow(0)
    private val mutableState = MutableStateFlow(State())
    val state: StateFlow<State> = mutableState.asStateFlow()

    init {
        scope.launch {
            combine(input.map(UserQuery::normalize).distinctUntilChanged(), attempts) { query, _ -> query }
                .collectLatest(::run)
        }
    }

    fun onInput(text: String) {
        input.value = text
    }

    /** Searches for the current query again, after an error. */
    fun retry() {
        attempts.update { it + 1 }
    }

    private suspend fun run(query: String) {
        val problem = UserQuery.check(query)
        if (problem != null) {
            mutableState.value = State(query, problem)
            return
        }
        mutableState.update { it.copy(query = query, problem = null, searching = true, error = null) }
        delay(delayMs)
        mutableState.value = try {
            State(query, problem = null, users = search(query))
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException) {
            State(query, problem = null, error = e)
        } catch (e: Exception) {
            State(query, problem = null, error = ApiException(0, ApiException.LOCAL, "local failure", cause = e))
        }
    }

    companion object {
        const val DELAY_MS = 300L
    }
}
