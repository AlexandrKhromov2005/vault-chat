-module(vault_calls_turn).
-moduledoc "TURN REST credentials bounded to one call participant and ten minutes.".

-export([issue/2]).

-spec issue(binary(), binary()) -> {ok, map()} | {error, unavailable}.
issue(CallId, User) ->
    case {application:get_env(vault_calls, turn_secret, undefined),
          application:get_env(vault_calls, turn_uris, undefined)} of
        {Secret, Uris} when is_binary(Secret), byte_size(Secret) > 0, is_list(Uris), Uris =/= [] ->
            Expiry = erlang:system_time(second) + 600,
            Username = iolist_to_binary([integer_to_binary(Expiry), <<":">>, CallId, <<":">>, User]),
            Credential = base64:encode(crypto:mac(hmac, sha, Secret, Username)),
            {ok, #{ttl => 600, ice_servers => [#{urls => Uris, username => Username, credential => Credential}]}};
        _ -> {error, unavailable}
    end.
