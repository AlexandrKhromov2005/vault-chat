package io.github.alexandrkhromov2005.vaultchat.core.net

import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.PUT
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

    /** Stores a message; a repeat with the same key returns the original (docs/chat/messages.md). */
    @POST("api/v1/rooms/{room_id}/messages")
    suspend fun sendMessage(
        @Header("Authorization") authorization: String,
        @Path("room_id") roomId: String,
        @Body body: SendMessageRequest,
    ): Response<MessageDto>

    @GET("api/v1/users/{user_id}")
    suspend fun user(@Header("Authorization") authorization: String, @Path("user_id") userId: String): Response<PublicProfile>

    /** Users whose name starts with [query], without the caller. */
    @GET("api/v1/users")
    suspend fun searchUsers(
        @Header("Authorization") authorization: String,
        @Query("query") query: String,
        @Query("limit") limit: Int,
    ): Response<UserPage>

    /** Returns the dialog with a user, creating it the first time; idempotent. */
    @POST("api/v1/rooms/direct")
    suspend fun openDirect(@Header("Authorization") authorization: String, @Body body: OpenDirectRequest): Response<RoomDto>

    /** Creates a channel; not idempotent, so OkHttp must not repeat it. */
    @POST("api/v1/rooms/channels")
    suspend fun createChannel(
        @Header("Authorization") authorization: String,
        @Body body: CreateChannelRequest,
        @Tag once: NotRepeatable,
    ): Response<RoomDto>

    @GET("api/v1/rooms/{room_id}/members")
    suspend fun members(
        @Header("Authorization") authorization: String,
        @Path("room_id") roomId: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int,
    ): Response<MemberPage>

    // The membership changes answer 204; see noContent.

    @PUT("api/v1/rooms/{room_id}/members/{user_id}")
    suspend fun addMember(
        @Header("Authorization") authorization: String,
        @Path("room_id") roomId: String,
        @Path("user_id") userId: String,
    ): Response<Unit>

    @DELETE("api/v1/rooms/{room_id}/members/{user_id}")
    suspend fun removeMember(
        @Header("Authorization") authorization: String,
        @Path("room_id") roomId: String,
        @Path("user_id") userId: String,
    ): Response<Unit>

    @DELETE("api/v1/rooms/{room_id}/members/me")
    suspend fun leave(@Header("Authorization") authorization: String, @Path("room_id") roomId: String): Response<Unit>
}

/**
 * Marks a request that the server would act on twice if it were repeated.
 * Its body is sent as one-shot, so OkHttp does not silently send it again
 * after a connection broke while it was being sent or answered.
 */
object NotRepeatable

/** A 204 answer as a [Unit] body, for calls that expect a body. */
fun Response<Unit>.noContent(): Response<Unit> =
    if (isSuccessful && body() == null) Response.success(Unit, raw()) else this

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
