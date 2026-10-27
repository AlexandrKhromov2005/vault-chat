package io.github.alexandrkhromov2005.vaultchat.core.net

import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query
import retrofit2.http.Tag

/**
 * The Gateway's REST API. Calls return [Response] so that [call] can map
 * errors uniformly and token lifetimes can be measured against the
 * response's `Date` header.
 */
interface GatewayApi {
    @POST("api/v1/auth/register")
    suspend fun register(@Body body: RegisterRequest): Response<Registration>

    @POST("api/v1/auth/login")
    suspend fun login(@Body body: LoginRequest): Response<TokenPair>

    @POST("api/v1/auth/email/verification/request")
    suspend fun requestEmailVerification(@Body body: EmailVerificationRequest): Response<EmailAcknowledgement>

    @POST("api/v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshTokenRequest, @Tag progress: RequestProgress): Response<TokenPair>

    @POST("api/v1/auth/logout")
    suspend fun logout(@Body body: RefreshTokenRequest): Response<Unit>

    @GET("api/v1/auth/me")
    suspend fun me(@Header("Authorization") authorization: String): Response<User>

    // Gateway rejects empty cursors and unknown parameters; Retrofit leaves
    // out null queries.

    @GET("api/v1/rooms")
    suspend fun rooms(
        @Header("Authorization") authorization: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int,
    ): Response<RoomPage>

    @GET("api/v1/rooms/{room_id}")
    suspend fun room(@Header("Authorization") authorization: String, @Path("room_id") roomId: String): Response<RoomDto>

    /** History newest first with [cursor], or catch-up oldest first with [after]; never both. */
    @GET("api/v1/rooms/{room_id}/messages")
    suspend fun messages(
        @Header("Authorization") authorization: String,
        @Path("room_id") roomId: String,
        @Query("cursor") cursor: String?,
        @Query("after") after: String?,
        @Query("limit") limit: Int,
    ): Response<MessagePage>

    @GET("api/v1/users/{user_id}")
    suspend fun user(@Header("Authorization") authorization: String, @Path("user_id") userId: String): Response<PublicProfile>
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
