package io.github.alexandrkhromov2005.vaultchat.core.net

import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Tag

/**
 * The Gateway's REST API. Calls return [Response] so that [call] can map
 * errors uniformly and token lifetimes can be measured against the
 * response's `Date` header.
 */
interface GatewayApi {
    @POST("api/v1/auth/register")
    suspend fun register(@Body body: RegisterRequest): Response<User>

    @POST("api/v1/auth/login")
    suspend fun login(@Body body: LoginRequest): Response<TokenPair>

    @POST("api/v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshTokenRequest, @Tag progress: RequestProgress): Response<TokenPair>

    @POST("api/v1/auth/logout")
    suspend fun logout(@Body body: RefreshTokenRequest): Response<Unit>

    @GET("api/v1/auth/me")
    suspend fun me(@Header("Authorization") authorization: String): Response<User>
}

/**
 * Whether a request started to leave the device. Set by [Gateway]'s refresh
 * client when it begins to write the request; a failure before that cannot
 * have reached the server.
 */
class RequestProgress {
    @Volatile
    var sent = false
        internal set
}

fun bearer(accessToken: String): String = "Bearer $accessToken"
