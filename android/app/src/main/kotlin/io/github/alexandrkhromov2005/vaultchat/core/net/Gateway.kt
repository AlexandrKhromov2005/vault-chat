package io.github.alexandrkhromov2005.vaultchat.core.net

import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.Json
import okhttp3.Call
import okhttp3.EventListener
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.ResponseBody
import okhttp3.ResponseBody.Companion.asResponseBody
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.Buffer
import okio.ForwardingSource
import okio.buffer
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit

/**
 * Creates and caches the Gateway APIs of each server.
 *
 * Every response body is limited to [MAX_RESPONSE_BYTES] while it is read, so
 * that a misbehaving server cannot exhaust memory. [refreshApi] serves only
 * token refresh: OkHttp must not silently repeat it, because each attempt
 * rotates the token, and its deadline must leave time to retry within the
 * server's retry window (see SessionManager).
 */
class Gateway(http: OkHttpClient, private val json: Json = GatewayJson) {
    // A WebSocket has no read deadline: the server pings every 30 s, and our
    // own pings drop a connection whose pong does not come back in time.
    private val webSocketHttp = http.newBuilder()
        .readTimeout(0, TimeUnit.SECONDS)
        .pingInterval(WEBSOCKET_PING_SECONDS, TimeUnit.SECONDS)
        .build()

    private val apiHttp = http.newBuilder().addInterceptor(ResponseSizeLimit(MAX_RESPONSE_BYTES)).build()
    private val refreshHttp = apiHttp.newBuilder()
        .retryOnConnectionFailure(false)
        .connectTimeout(REFRESH_CONNECT_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .callTimeout(REFRESH_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .eventListenerFactory { call ->
            val progress = call.request().tag(RequestProgress::class.java)
            if (progress == null) EventListener.NONE else RequestSentListener(progress)
        }
        .build()

    private val apis = ConcurrentHashMap<ServerAddress, GatewayApi>()
    private val refreshApis = ConcurrentHashMap<ServerAddress, GatewayApi>()

    fun api(server: ServerAddress): GatewayApi = apis.getOrPut(server) { create(server, apiHttp) }

    fun refreshApi(server: ServerAddress): GatewayApi = refreshApis.getOrPut(server) { create(server, refreshHttp) }

    /** Opens the event stream; the token is sent in the first frame, never in the URL. */
    fun webSocket(server: ServerAddress, listener: WebSocketListener): WebSocket =
        webSocketHttp.newWebSocket(Request.Builder().url(server.url.resolve("api/v1/ws")!!).build(), listener)

    private fun create(server: ServerAddress, client: OkHttpClient): GatewayApi = Retrofit.Builder()
        .baseUrl(server.url)
        .client(client)
        .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
        .build()
        .create(GatewayApi::class.java)

    companion object {
        /** API responses are small JSON documents; files will use their own client. */
        const val MAX_RESPONSE_BYTES = 1024L * 1024

        /** Gateway's own budget is 10 s; a refresh that takes longer is retried. */
        const val REFRESH_TIMEOUT_SECONDS = 10L

        /** Our pings; the Gateway drops connections that miss its own pings within 10 s. */
        const val WEBSOCKET_PING_SECONDS = 30L

        // Leaves time for an immediate repeat within the call deadline.
        private const val REFRESH_CONNECT_TIMEOUT_SECONDS = 5L
    }
}

val GatewayJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
}

/** A successful response: its body and the server's `Date` header, if any. */
class Success<T>(val body: T, val serverDateMillis: Long?)

/**
 * Runs a Gateway request and returns its successful body, or throws
 * [ApiException] for an error response, a broken connection or a response
 * that is not what the API describes.
 */
suspend fun <T : Any> call(request: suspend () -> Response<T>): Success<T> {
    val response = execute(request)
    val body = response.body()
        ?: throw ApiException(response.code(), ApiException.INVALID_RESPONSE, "empty response")
    return Success(body, response.headers().getDate("Date")?.time)
}

/** Like [call], for requests answered with 204 No Content. */
suspend fun callNoContent(request: suspend () -> Response<Unit>) {
    execute(request)
}

/** Sends the request; returns only successful responses. */
private suspend fun <T> execute(request: suspend () -> Response<T>): Response<T> {
    val response = try {
        request()
    } catch (e: CancellationException) {
        throw e
    } catch (e: ResponseTooLargeException) {
        throw ApiException(0, ApiException.INVALID_RESPONSE, "response too large", cause = e)
    } catch (e: IOException) {
        throw ApiException(0, ApiException.NETWORK, "network failure", cause = e)
    } catch (e: IllegalArgumentException) {
        // kotlinx.serialization reports malformed JSON and missing fields
        // with SerializationException, a subclass.
        throw ApiException(200, ApiException.INVALID_RESPONSE, "malformed response", cause = e)
    }
    if (!response.isSuccessful) throw response.toApiException()
    return response
}

// An error envelope is a few hundred bytes; a longer error body is not parsed.
private const val MAX_ERROR_BODY_BYTES = 16L * 1024

internal fun Response<*>.toApiException(): ApiException {
    val envelope = errorBody()?.let(::parseEnvelope)
    return ApiException(
        status = code(),
        code = envelope?.code ?: ApiException.HTTP_ERROR,
        message = envelope?.message ?: "HTTP ${code()}",
        retryAfterSeconds = headers()["Retry-After"]?.trim()?.toLongOrNull()?.takeIf { it >= 0 },
    )
}

private fun parseEnvelope(body: ResponseBody): ErrorBody? = try {
    body.use {
        val source = it.source()
        if (source.request(MAX_ERROR_BODY_BYTES + 1)) {
            null
        } else {
            GatewayJson.decodeFromString(ErrorEnvelope.serializer(), source.readUtf8()).error
        }
    }
} catch (e: IOException) {
    null
} catch (e: IllegalArgumentException) {
    // Not the Gateway's envelope.
    null
}

private class RequestSentListener(private val progress: RequestProgress) : EventListener() {
    override fun requestHeadersStart(call: Call) {
        progress.sent = true
    }
}

class ResponseTooLargeException(limit: Long) : IOException("response body exceeds $limit bytes")

/**
 * Fails reading a response body beyond [maxBytes]. It counts decoded bytes,
 * so a compressed body cannot expand past the limit either.
 */
internal class ResponseSizeLimit(private val maxBytes: Long) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): okhttp3.Response {
        val response = chain.proceed(chain.request())
        val body = response.body
        if (body.contentLength() > maxBytes) {
            response.close()
            throw ResponseTooLargeException(maxBytes)
        }
        val limited = object : ForwardingSource(body.source()) {
            private var total = 0L

            override fun read(sink: Buffer, byteCount: Long): Long {
                val read = super.read(sink, byteCount)
                if (read > 0) {
                    total += read
                    if (total > maxBytes) throw ResponseTooLargeException(maxBytes)
                }
                return read
            }
        }
        return response.newBuilder()
            .body(limited.buffer().asResponseBody(body.contentType(), body.contentLength()))
            .build()
    }
}
