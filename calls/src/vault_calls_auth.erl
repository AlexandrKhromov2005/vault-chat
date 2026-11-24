-module(vault_calls_auth).
-moduledoc "Production Auth adapter: every Calls request revalidates its access token.".

-export([validate/1, validate_session/2]).

-spec validate(binary()) -> {ok, {binary(), binary()}} | {error, unauthorized | unavailable}.
validate(Token) -> vault_calls_metrics:dependency(auth, fun() -> validate_request(Token) end).

-spec validate_request(binary()) -> {ok, {binary(), binary()}} | {error, unauthorized | unavailable}.
validate_request(Token) when is_binary(Token), byte_size(Token) > 0, byte_size(Token) =< 4096 ->
    Ctx = ctx:with_deadline_after(ctx:new(), 2000, millisecond),
    case auth_v_1_auth_service_client:validate_token(Ctx, #{token => Token}, #{channel => calls_auth}) of
        {ok, #{valid := true, user_id := User, session_id := Session}, _Metadata}
          when is_binary(User), byte_size(User) > 0, byte_size(User) =< 256,
               is_binary(Session), byte_size(Session) > 0, byte_size(Session) =< 256 ->
            {ok, {User, Session}};
        {ok, _, _} -> {error, unauthorized};
        _ -> {error, unavailable}
    end;
validate_request(_) -> {error, unauthorized}.

-spec validate_session(binary(), binary()) -> ok | {error, revoked | unavailable}.
validate_session(User, Session) ->
    vault_calls_metrics:dependency(auth, fun() -> validate_session_request(User, Session) end).

-spec validate_session_request(binary(), binary()) -> ok | {error, revoked | unavailable}.
validate_session_request(User, Session) when is_binary(User), is_binary(Session) ->
    Ctx = ctx:with_deadline_after(ctx:new(), 2000, millisecond),
    case auth_v_1_auth_service_client:validate_session(Ctx, #{user_id => User, session_id => Session},
                                                      #{channel => calls_auth}) of
        {ok, #{active := true}, _} -> ok;
        {ok, #{active := false}, _} -> {error, revoked};
        _ -> {error, unavailable}
    end;
validate_session_request(_, _) -> {error, revoked}.
