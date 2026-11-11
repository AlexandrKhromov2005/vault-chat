-module(vault_calls_auth_stub).
-moduledoc "Configurable synthetic Auth adapter for the loopback prototype.".

-export([validate/1]).

-spec validate(binary()) -> {ok, {binary(), binary()}} | {error, unauthorized | unavailable}.
validate(Token) ->
    ok = delay(auth_delay_ms),
    case application:get_env(vault_calls, auth_available, true) of
        false -> {error, unavailable};
        true -> validate_token(Token)
    end.

-spec validate_token(binary()) -> {ok, {binary(), binary()}} | {error, unauthorized}.
validate_token(Token) ->
    case binary:split(Token, <<":">>, [global]) of
        [<<"prototype">>, User, Session] when byte_size(User) > 0, byte_size(User) =< 256,
                                              byte_size(Session) > 0, byte_size(Session) =< 256 ->
            {ok, {User, Session}};
        _ -> {error, unauthorized}
    end.

-spec delay(atom()) -> ok.
delay(Name) ->
    Milliseconds = application:get_env(vault_calls, Name, 0),
    timer:sleep(min(Milliseconds, 2000)),
    ok.
