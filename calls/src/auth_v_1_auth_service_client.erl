%%%-------------------------------------------------------------------
%% @doc Client module for grpc service auth.v1.AuthService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(auth_v_1_auth_service_client).

-compile(export_all).
-compile(nowarn_export_all).

-include_lib("grpcbox/include/grpcbox.hrl").

-define(is_ctx(Ctx), is_tuple(Ctx) andalso element(1, Ctx) =:= ctx).

-define(SERVICE, 'auth.v1.AuthService').
-define(PROTO_MODULE, 'auth_pb').
-define(MARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:encode_msg(I, T) end).
-define(UNMARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:decode_msg(I, T) end).
-define(DEF(Input, Output, MessageType), #grpcbox_def{service=?SERVICE,
                                                      message_type=MessageType,
                                                      marshal_fun=?MARSHAL_FUN(Input),
                                                      unmarshal_fun=?UNMARSHAL_FUN(Output)}).

%% @doc Unary RPC
-spec get_user(auth_pb:get_user_request()) ->
    {ok, auth_pb:get_user_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_user(Input) ->
    get_user(ctx:new(), Input, #{}).

-spec get_user(ctx:t() | auth_pb:get_user_request(), auth_pb:get_user_request() | grpcbox_client:options()) ->
    {ok, auth_pb:get_user_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_user(Ctx, Input) when ?is_ctx(Ctx) ->
    get_user(Ctx, Input, #{});
get_user(Input, Options) ->
    get_user(ctx:new(), Input, Options).

-spec get_user(ctx:t(), auth_pb:get_user_request(), grpcbox_client:options()) ->
    {ok, auth_pb:get_user_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_user(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/GetUser">>, Input, ?DEF(get_user_request, get_user_response, <<"auth.v1.GetUserRequest">>), Options).

%% @doc Unary RPC
-spec search_users(auth_pb:search_users_request()) ->
    {ok, auth_pb:search_users_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
search_users(Input) ->
    search_users(ctx:new(), Input, #{}).

-spec search_users(ctx:t() | auth_pb:search_users_request(), auth_pb:search_users_request() | grpcbox_client:options()) ->
    {ok, auth_pb:search_users_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
search_users(Ctx, Input) when ?is_ctx(Ctx) ->
    search_users(Ctx, Input, #{});
search_users(Input, Options) ->
    search_users(ctx:new(), Input, Options).

-spec search_users(ctx:t(), auth_pb:search_users_request(), grpcbox_client:options()) ->
    {ok, auth_pb:search_users_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
search_users(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/SearchUsers">>, Input, ?DEF(search_users_request, search_users_response, <<"auth.v1.SearchUsersRequest">>), Options).

%% @doc Unary RPC
-spec register(auth_pb:register_request()) ->
    {ok, auth_pb:register_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
register(Input) ->
    register(ctx:new(), Input, #{}).

-spec register(ctx:t() | auth_pb:register_request(), auth_pb:register_request() | grpcbox_client:options()) ->
    {ok, auth_pb:register_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
register(Ctx, Input) when ?is_ctx(Ctx) ->
    register(Ctx, Input, #{});
register(Input, Options) ->
    register(ctx:new(), Input, Options).

-spec register(ctx:t(), auth_pb:register_request(), grpcbox_client:options()) ->
    {ok, auth_pb:register_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
register(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/Register">>, Input, ?DEF(register_request, register_response, <<"auth.v1.RegisterRequest">>), Options).

%% @doc Unary RPC
-spec login(auth_pb:login_request()) ->
    {ok, auth_pb:login_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
login(Input) ->
    login(ctx:new(), Input, #{}).

-spec login(ctx:t() | auth_pb:login_request(), auth_pb:login_request() | grpcbox_client:options()) ->
    {ok, auth_pb:login_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
login(Ctx, Input) when ?is_ctx(Ctx) ->
    login(Ctx, Input, #{});
login(Input, Options) ->
    login(ctx:new(), Input, Options).

-spec login(ctx:t(), auth_pb:login_request(), grpcbox_client:options()) ->
    {ok, auth_pb:login_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
login(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/Login">>, Input, ?DEF(login_request, login_response, <<"auth.v1.LoginRequest">>), Options).

%% @doc Unary RPC
-spec validate_token(auth_pb:validate_token_request()) ->
    {ok, auth_pb:validate_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
validate_token(Input) ->
    validate_token(ctx:new(), Input, #{}).

-spec validate_token(ctx:t() | auth_pb:validate_token_request(), auth_pb:validate_token_request() | grpcbox_client:options()) ->
    {ok, auth_pb:validate_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
validate_token(Ctx, Input) when ?is_ctx(Ctx) ->
    validate_token(Ctx, Input, #{});
validate_token(Input, Options) ->
    validate_token(ctx:new(), Input, Options).

-spec validate_token(ctx:t(), auth_pb:validate_token_request(), grpcbox_client:options()) ->
    {ok, auth_pb:validate_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
validate_token(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/ValidateToken">>, Input, ?DEF(validate_token_request, validate_token_response, <<"auth.v1.ValidateTokenRequest">>), Options).

%% @doc Unary RPC
-spec validate_session(auth_pb:validate_session_request()) ->
    {ok, auth_pb:validate_session_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
validate_session(Input) ->
    validate_session(ctx:new(), Input, #{}).

-spec validate_session(ctx:t() | auth_pb:validate_session_request(), auth_pb:validate_session_request() | grpcbox_client:options()) ->
    {ok, auth_pb:validate_session_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
validate_session(Ctx, Input) when ?is_ctx(Ctx) ->
    validate_session(Ctx, Input, #{});
validate_session(Input, Options) ->
    validate_session(ctx:new(), Input, Options).

-spec validate_session(ctx:t(), auth_pb:validate_session_request(), grpcbox_client:options()) ->
    {ok, auth_pb:validate_session_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
validate_session(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/ValidateSession">>, Input, ?DEF(validate_session_request, validate_session_response, <<"auth.v1.ValidateSessionRequest">>), Options).

%% @doc Unary RPC
-spec refresh_token(auth_pb:refresh_token_request()) ->
    {ok, auth_pb:refresh_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
refresh_token(Input) ->
    refresh_token(ctx:new(), Input, #{}).

-spec refresh_token(ctx:t() | auth_pb:refresh_token_request(), auth_pb:refresh_token_request() | grpcbox_client:options()) ->
    {ok, auth_pb:refresh_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
refresh_token(Ctx, Input) when ?is_ctx(Ctx) ->
    refresh_token(Ctx, Input, #{});
refresh_token(Input, Options) ->
    refresh_token(ctx:new(), Input, Options).

-spec refresh_token(ctx:t(), auth_pb:refresh_token_request(), grpcbox_client:options()) ->
    {ok, auth_pb:refresh_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
refresh_token(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/RefreshToken">>, Input, ?DEF(refresh_token_request, refresh_token_response, <<"auth.v1.RefreshTokenRequest">>), Options).

%% @doc Unary RPC
-spec logout(auth_pb:logout_request()) ->
    {ok, auth_pb:logout_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
logout(Input) ->
    logout(ctx:new(), Input, #{}).

-spec logout(ctx:t() | auth_pb:logout_request(), auth_pb:logout_request() | grpcbox_client:options()) ->
    {ok, auth_pb:logout_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
logout(Ctx, Input) when ?is_ctx(Ctx) ->
    logout(Ctx, Input, #{});
logout(Input, Options) ->
    logout(ctx:new(), Input, Options).

-spec logout(ctx:t(), auth_pb:logout_request(), grpcbox_client:options()) ->
    {ok, auth_pb:logout_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
logout(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/Logout">>, Input, ?DEF(logout_request, logout_response, <<"auth.v1.LogoutRequest">>), Options).

%% @doc Unary RPC
-spec revoke_all_sessions(auth_pb:revoke_all_sessions_request()) ->
    {ok, auth_pb:revoke_all_sessions_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
revoke_all_sessions(Input) ->
    revoke_all_sessions(ctx:new(), Input, #{}).

-spec revoke_all_sessions(ctx:t() | auth_pb:revoke_all_sessions_request(), auth_pb:revoke_all_sessions_request() | grpcbox_client:options()) ->
    {ok, auth_pb:revoke_all_sessions_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
revoke_all_sessions(Ctx, Input) when ?is_ctx(Ctx) ->
    revoke_all_sessions(Ctx, Input, #{});
revoke_all_sessions(Input, Options) ->
    revoke_all_sessions(ctx:new(), Input, Options).

-spec revoke_all_sessions(ctx:t(), auth_pb:revoke_all_sessions_request(), grpcbox_client:options()) ->
    {ok, auth_pb:revoke_all_sessions_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
revoke_all_sessions(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/RevokeAllSessions">>, Input, ?DEF(revoke_all_sessions_request, revoke_all_sessions_response, <<"auth.v1.RevokeAllSessionsRequest">>), Options).

%% @doc Unary RPC
-spec get_account(auth_pb:get_account_request()) ->
    {ok, auth_pb:get_account_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_account(Input) ->
    get_account(ctx:new(), Input, #{}).

-spec get_account(ctx:t() | auth_pb:get_account_request(), auth_pb:get_account_request() | grpcbox_client:options()) ->
    {ok, auth_pb:get_account_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_account(Ctx, Input) when ?is_ctx(Ctx) ->
    get_account(Ctx, Input, #{});
get_account(Input, Options) ->
    get_account(ctx:new(), Input, Options).

-spec get_account(ctx:t(), auth_pb:get_account_request(), grpcbox_client:options()) ->
    {ok, auth_pb:get_account_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_account(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/GetAccount">>, Input, ?DEF(get_account_request, get_account_response, <<"auth.v1.GetAccountRequest">>), Options).

%% @doc Unary RPC
-spec request_email_verification(auth_pb:request_email_verification_request()) ->
    {ok, auth_pb:request_email_verification_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_email_verification(Input) ->
    request_email_verification(ctx:new(), Input, #{}).

-spec request_email_verification(ctx:t() | auth_pb:request_email_verification_request(), auth_pb:request_email_verification_request() | grpcbox_client:options()) ->
    {ok, auth_pb:request_email_verification_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_email_verification(Ctx, Input) when ?is_ctx(Ctx) ->
    request_email_verification(Ctx, Input, #{});
request_email_verification(Input, Options) ->
    request_email_verification(ctx:new(), Input, Options).

-spec request_email_verification(ctx:t(), auth_pb:request_email_verification_request(), grpcbox_client:options()) ->
    {ok, auth_pb:request_email_verification_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_email_verification(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/RequestEmailVerification">>, Input, ?DEF(request_email_verification_request, request_email_verification_response, <<"auth.v1.RequestEmailVerificationRequest">>), Options).

%% @doc Unary RPC
-spec confirm_email_verification(auth_pb:confirm_email_verification_request()) ->
    {ok, auth_pb:confirm_email_verification_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_email_verification(Input) ->
    confirm_email_verification(ctx:new(), Input, #{}).

-spec confirm_email_verification(ctx:t() | auth_pb:confirm_email_verification_request(), auth_pb:confirm_email_verification_request() | grpcbox_client:options()) ->
    {ok, auth_pb:confirm_email_verification_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_email_verification(Ctx, Input) when ?is_ctx(Ctx) ->
    confirm_email_verification(Ctx, Input, #{});
confirm_email_verification(Input, Options) ->
    confirm_email_verification(ctx:new(), Input, Options).

-spec confirm_email_verification(ctx:t(), auth_pb:confirm_email_verification_request(), grpcbox_client:options()) ->
    {ok, auth_pb:confirm_email_verification_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_email_verification(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/ConfirmEmailVerification">>, Input, ?DEF(confirm_email_verification_request, confirm_email_verification_response, <<"auth.v1.ConfirmEmailVerificationRequest">>), Options).

%% @doc Unary RPC
-spec request_password_reset(auth_pb:request_password_reset_request()) ->
    {ok, auth_pb:request_password_reset_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_password_reset(Input) ->
    request_password_reset(ctx:new(), Input, #{}).

-spec request_password_reset(ctx:t() | auth_pb:request_password_reset_request(), auth_pb:request_password_reset_request() | grpcbox_client:options()) ->
    {ok, auth_pb:request_password_reset_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_password_reset(Ctx, Input) when ?is_ctx(Ctx) ->
    request_password_reset(Ctx, Input, #{});
request_password_reset(Input, Options) ->
    request_password_reset(ctx:new(), Input, Options).

-spec request_password_reset(ctx:t(), auth_pb:request_password_reset_request(), grpcbox_client:options()) ->
    {ok, auth_pb:request_password_reset_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_password_reset(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/RequestPasswordReset">>, Input, ?DEF(request_password_reset_request, request_password_reset_response, <<"auth.v1.RequestPasswordResetRequest">>), Options).

%% @doc Unary RPC
-spec confirm_password_reset(auth_pb:confirm_password_reset_request()) ->
    {ok, auth_pb:confirm_password_reset_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_password_reset(Input) ->
    confirm_password_reset(ctx:new(), Input, #{}).

-spec confirm_password_reset(ctx:t() | auth_pb:confirm_password_reset_request(), auth_pb:confirm_password_reset_request() | grpcbox_client:options()) ->
    {ok, auth_pb:confirm_password_reset_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_password_reset(Ctx, Input) when ?is_ctx(Ctx) ->
    confirm_password_reset(Ctx, Input, #{});
confirm_password_reset(Input, Options) ->
    confirm_password_reset(ctx:new(), Input, Options).

-spec confirm_password_reset(ctx:t(), auth_pb:confirm_password_reset_request(), grpcbox_client:options()) ->
    {ok, auth_pb:confirm_password_reset_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_password_reset(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/ConfirmPasswordReset">>, Input, ?DEF(confirm_password_reset_request, confirm_password_reset_response, <<"auth.v1.ConfirmPasswordResetRequest">>), Options).

%% @doc Unary RPC
-spec request_email_change(auth_pb:request_email_change_request()) ->
    {ok, auth_pb:request_email_change_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_email_change(Input) ->
    request_email_change(ctx:new(), Input, #{}).

-spec request_email_change(ctx:t() | auth_pb:request_email_change_request(), auth_pb:request_email_change_request() | grpcbox_client:options()) ->
    {ok, auth_pb:request_email_change_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_email_change(Ctx, Input) when ?is_ctx(Ctx) ->
    request_email_change(Ctx, Input, #{});
request_email_change(Input, Options) ->
    request_email_change(ctx:new(), Input, Options).

-spec request_email_change(ctx:t(), auth_pb:request_email_change_request(), grpcbox_client:options()) ->
    {ok, auth_pb:request_email_change_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
request_email_change(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/RequestEmailChange">>, Input, ?DEF(request_email_change_request, request_email_change_response, <<"auth.v1.RequestEmailChangeRequest">>), Options).

%% @doc Unary RPC
-spec confirm_email_change(auth_pb:confirm_email_change_request()) ->
    {ok, auth_pb:confirm_email_change_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_email_change(Input) ->
    confirm_email_change(ctx:new(), Input, #{}).

-spec confirm_email_change(ctx:t() | auth_pb:confirm_email_change_request(), auth_pb:confirm_email_change_request() | grpcbox_client:options()) ->
    {ok, auth_pb:confirm_email_change_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_email_change(Ctx, Input) when ?is_ctx(Ctx) ->
    confirm_email_change(Ctx, Input, #{});
confirm_email_change(Input, Options) ->
    confirm_email_change(ctx:new(), Input, Options).

-spec confirm_email_change(ctx:t(), auth_pb:confirm_email_change_request(), grpcbox_client:options()) ->
    {ok, auth_pb:confirm_email_change_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
confirm_email_change(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/ConfirmEmailChange">>, Input, ?DEF(confirm_email_change_request, confirm_email_change_response, <<"auth.v1.ConfirmEmailChangeRequest">>), Options).

%% @doc Unary RPC
-spec register_push_token(auth_pb:register_push_token_request()) ->
    {ok, auth_pb:register_push_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
register_push_token(Input) ->
    register_push_token(ctx:new(), Input, #{}).

-spec register_push_token(ctx:t() | auth_pb:register_push_token_request(), auth_pb:register_push_token_request() | grpcbox_client:options()) ->
    {ok, auth_pb:register_push_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
register_push_token(Ctx, Input) when ?is_ctx(Ctx) ->
    register_push_token(Ctx, Input, #{});
register_push_token(Input, Options) ->
    register_push_token(ctx:new(), Input, Options).

-spec register_push_token(ctx:t(), auth_pb:register_push_token_request(), grpcbox_client:options()) ->
    {ok, auth_pb:register_push_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
register_push_token(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/RegisterPushToken">>, Input, ?DEF(register_push_token_request, register_push_token_response, <<"auth.v1.RegisterPushTokenRequest">>), Options).

%% @doc Unary RPC
-spec unregister_push_token(auth_pb:unregister_push_token_request()) ->
    {ok, auth_pb:unregister_push_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
unregister_push_token(Input) ->
    unregister_push_token(ctx:new(), Input, #{}).

-spec unregister_push_token(ctx:t() | auth_pb:unregister_push_token_request(), auth_pb:unregister_push_token_request() | grpcbox_client:options()) ->
    {ok, auth_pb:unregister_push_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
unregister_push_token(Ctx, Input) when ?is_ctx(Ctx) ->
    unregister_push_token(Ctx, Input, #{});
unregister_push_token(Input, Options) ->
    unregister_push_token(ctx:new(), Input, Options).

-spec unregister_push_token(ctx:t(), auth_pb:unregister_push_token_request(), grpcbox_client:options()) ->
    {ok, auth_pb:unregister_push_token_response(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
unregister_push_token(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/auth.v1.AuthService/UnregisterPushToken">>, Input, ?DEF(unregister_push_token_request, unregister_push_token_response, <<"auth.v1.UnregisterPushTokenRequest">>), Options).

