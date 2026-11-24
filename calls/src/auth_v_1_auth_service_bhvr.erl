%%%-------------------------------------------------------------------
%% @doc Behaviour to implement for grpc service auth.v1.AuthService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(auth_v_1_auth_service_bhvr).

%% Unary RPC
-callback get_user(ctx:t(), auth_pb:get_user_request()) ->
    {ok, auth_pb:get_user_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback search_users(ctx:t(), auth_pb:search_users_request()) ->
    {ok, auth_pb:search_users_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback register(ctx:t(), auth_pb:register_request()) ->
    {ok, auth_pb:register_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback login(ctx:t(), auth_pb:login_request()) ->
    {ok, auth_pb:login_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback validate_token(ctx:t(), auth_pb:validate_token_request()) ->
    {ok, auth_pb:validate_token_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback validate_session(ctx:t(), auth_pb:validate_session_request()) ->
    {ok, auth_pb:validate_session_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback refresh_token(ctx:t(), auth_pb:refresh_token_request()) ->
    {ok, auth_pb:refresh_token_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback logout(ctx:t(), auth_pb:logout_request()) ->
    {ok, auth_pb:logout_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback revoke_all_sessions(ctx:t(), auth_pb:revoke_all_sessions_request()) ->
    {ok, auth_pb:revoke_all_sessions_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback get_account(ctx:t(), auth_pb:get_account_request()) ->
    {ok, auth_pb:get_account_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback request_email_verification(ctx:t(), auth_pb:request_email_verification_request()) ->
    {ok, auth_pb:request_email_verification_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback confirm_email_verification(ctx:t(), auth_pb:confirm_email_verification_request()) ->
    {ok, auth_pb:confirm_email_verification_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback request_password_reset(ctx:t(), auth_pb:request_password_reset_request()) ->
    {ok, auth_pb:request_password_reset_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback confirm_password_reset(ctx:t(), auth_pb:confirm_password_reset_request()) ->
    {ok, auth_pb:confirm_password_reset_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback request_email_change(ctx:t(), auth_pb:request_email_change_request()) ->
    {ok, auth_pb:request_email_change_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback confirm_email_change(ctx:t(), auth_pb:confirm_email_change_request()) ->
    {ok, auth_pb:confirm_email_change_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback register_push_token(ctx:t(), auth_pb:register_push_token_request()) ->
    {ok, auth_pb:register_push_token_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback unregister_push_token(ctx:t(), auth_pb:unregister_push_token_request()) ->
    {ok, auth_pb:unregister_push_token_response(), ctx:t()} | grpcbox_stream:grpc_error_response().

