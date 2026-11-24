-module(vault_calls_rpc_tests).

-include_lib("eunit/include/eunit.hrl").

rpc_test_() ->
    {foreach, fun setup/0, fun cleanup/1, [
        fun(Setup) -> fun() -> lifecycle_and_reconciliation(Setup) end end,
        fun(Setup) -> fun() -> identity_cannot_be_forged(Setup) end end,
        fun(Setup) -> fun() -> every_command_checks_dependencies(Setup) end end,
        fun(Setup) -> fun() -> monitor_ends_revoked_or_removed_calls(Setup) end end,
        fun(Setup) -> fun() -> turn_credentials_are_bounded(Setup) end end,
        fun(Setup) -> fun() -> drain_rejects_create(Setup) end end
    ]}.

setup() ->
    Url = case os:getenv("VAULT_CHAT_TEST_CALLS_DATABASE_URL") of
        false -> error(database_url_required);
        Value -> unicode:characters_to_binary(Value)
    end,
    _ = application:load(vault_calls),
    {ok, Store} = vault_calls_store:start_link(Url),
    ok = vault_calls_store:migrate(Store),
    {ok, Connection} = vault_calls_store:connect(Url),
    [{ok, [], []}, {ok, 1}] = epgsql:squery(Connection,
        "TRUNCATE call_commands, call_signals, call_participants, calls; UPDATE call_capacity SET active_calls = 0"),
    ok = epgsql:close(Connection),
    ok = vault_calls_store:stop(Store),
    [ok = application:set_env(vault_calls, Key, Value) || {Key, Value} <-
        [{database_url, Url}, {ringing_timeout, 60000}, {connecting_timeout, 60000},
         {heartbeat_timeout, 60000}, {max_call_duration, 60000},
         {http_enabled, false}, {auth_available, true}, {chat_available, true},
         {auth_mod, vault_calls_auth_stub}, {chat_mod, vault_calls_chat_stub},
         {rooms, #{<<"room-1">> => [<<"alice">>, <<"bob">>]}}]],
    {ok, _} = application:ensure_all_started(vault_calls),
    ok.

cleanup(_) ->
    ok = application:stop(vault_calls),
    ok = application:unload(vault_calls).

lifecycle_and_reconciliation(_) ->
    Created = create(identity(<<"alice">>, <<"phone">>), <<"create-1">>),
    Call = response_call(Created),
    Id = maps:get(call_id, Call),
    ?assertEqual(<<"ringing">>, maps:get(state, Call)),
    ?assertEqual(Call, response_call(create(identity(<<"alice">>, <<"phone">>), <<"create-1">>))),
    Accepted = vault_calls_rpc:accept(ctx:new(), #{
        identity => identity(<<"bob">>, <<"laptop">>), call_id => Id, command_id => <<"accept-1">>}),
    ?assertEqual(<<"connecting">>, maps:get(state, response_call(Accepted))),
    Active = vault_calls_rpc:list_active(ctx:new(), #{identity => identity(<<"bob">>, <<"laptop">>)}),
    {ok, #{calls := Calls}, _} = Active,
    ?assert(lists:any(fun(Item) -> maps:get(call_id, Item) =:= Id end, Calls)),
    Signaled = vault_calls_rpc:signal(ctx:new(), #{
        identity => identity(<<"alice">>, <<"phone">>), call_id => Id,
        kind => <<"offer">>, payload => <<"sdp">>, sender_seq => 1}),
    ?assertMatch({ok, #{state := <<"connecting">>, signal_seq := 1}, _}, Signaled),
    {ok, #{signals := [Signal]}, _} = vault_calls_rpc:signals(ctx:new(), #{
        identity => identity(<<"bob">>, <<"laptop">>), call_id => Id, 'after' => 0}),
    ?assertEqual(#{signal_seq => 1, sender_device => <<"phone">>, sender_seq => 1,
                   kind => <<"offer">>, payload => <<"sdp">>}, Signal),
    {ok, Ended, _} = vault_calls_rpc:hangup(ctx:new(), #{
        identity => identity(<<"bob">>, <<"laptop">>), call_id => Id, command_id => <<"end-1">>}),
    ?assertEqual(<<"ended">>, maps:get(state, maps:get(call, Ended))),
    ?assertMatch({grpc_error, {<<"5">>, <<"not_found">>}},
                 vault_calls_rpc:get(ctx:new(), #{identity => identity(<<"bob">>, <<"laptop">>), call_id => Id})).

identity_cannot_be_forged(_) ->
    ForgedUser = create(#{access_token => <<"prototype:alice:phone">>, user_id => <<"mallory">>, session_id => <<"phone">>}, <<"forged-user">>),
    ForgedSession = create(#{access_token => <<"prototype:alice:phone">>, user_id => <<"alice">>, session_id => <<"other">>}, <<"forged-session">>),
    ?assertEqual({grpc_error, {<<"16">>, <<"unauthorized">>}}, ForgedUser),
    ?assertEqual({grpc_error, {<<"16">>, <<"unauthorized">>}}, ForgedSession),
    ?assertEqual({ok, []}, vault_calls_store:list_active(vault_calls_store)).

every_command_checks_dependencies(_) ->
    {ok, Created, _} = create(identity(<<"alice">>, <<"phone">>), <<"create-1">>),
    Id = maps:get(call_id, maps:get(call, Created)),
    ok = application:set_env(vault_calls, chat_available, false),
    ?assertEqual({grpc_error, {<<"14">>, <<"unavailable">>}}, vault_calls_rpc:heartbeat(ctx:new(), #{
        identity => identity(<<"alice">>, <<"phone">>), call_id => Id, command_id => <<"heartbeat-1">>})),
    ok = application:set_env(vault_calls, chat_available, true),
    ok = application:set_env(vault_calls, auth_available, false),
    ?assertEqual({grpc_error, {<<"14">>, <<"unavailable">>}}, vault_calls_rpc:list_active(ctx:new(), #{
        identity => identity(<<"alice">>, <<"phone">>)})).

turn_credentials_are_bounded(_) ->
    ok = application:set_env(vault_calls, turn_secret, <<"synthetic-turn-secret">>),
    ok = application:set_env(vault_calls, turn_uris, [<<"turn:turn.example.test:3478?transport=udp">>]),
    {ok, Created, _} = create(identity(<<"alice">>, <<"phone">>), <<"create-1">>),
    Id = maps:get(call_id, maps:get(call, Created)),
    Request = #{identity => identity(<<"alice">>, <<"phone">>), call_id => Id},
    ?assertEqual({grpc_error, {<<"9">>, <<"conflict">>}}, vault_calls_rpc:turn(ctx:new(), Request)),
    {ok, _, _} = vault_calls_rpc:accept(ctx:new(), #{
        identity => identity(<<"bob">>, <<"laptop">>), call_id => Id, command_id => <<"accept-1">>}),
    {ok, #{ttl := 600, ice_servers := [Server]}, _} = vault_calls_rpc:turn(ctx:new(), Request),
    ?assertEqual([<<"turn:turn.example.test:3478?transport=udp">>], maps:get(urls, Server)),
    Username = maps:get(username, Server),
    [Expiry, Id, <<"alice">>] = binary:split(Username, <<":">>, [global]),
    ?assert(abs(binary_to_integer(Expiry) - erlang:system_time(second) - 600) =< 2),
    ?assertEqual(base64:encode(crypto:mac(hmac, sha, <<"synthetic-turn-secret">>, Username)),
                 maps:get(credential, Server)),
    {ok, _, _} = vault_calls_rpc:hangup(ctx:new(), #{
        identity => identity(<<"alice">>, <<"phone">>), call_id => Id, command_id => <<"end-1">>}),
    ?assertEqual({grpc_error, {<<"5">>, <<"not_found">>}}, vault_calls_rpc:turn(ctx:new(), Request)).

drain_rejects_create(_) ->
    ok = application:set_env(vault_calls, draining, true),
    ?assertEqual({grpc_error, {<<"14">>, <<"capacity">>}},
        create(identity(<<"alice">>, <<"phone">>), <<"drain-create">>)),
    ?assertEqual({ok, []}, vault_calls_store:list_active(vault_calls_store)).

monitor_ends_revoked_or_removed_calls(_) ->
    {ok, Monitor} = vault_calls_monitor:start_link(),
    {ok, Created, _} = create(identity(<<"alice">>, <<"phone">>), <<"create-1">>),
    Id = maps:get(call_id, maps:get(call, Created)),
    ok = application:set_env(vault_calls, revoked_sessions, [{<<"alice">>, <<"phone">>}]),
    ok = vault_calls_monitor:check_now(),
    ok = wait_missing(Id, 100),
    ok = application:set_env(vault_calls, revoked_sessions, []),
    {ok, Second, _} = create(identity(<<"alice">>, <<"phone">>), <<"create-2">>),
    SecondId = maps:get(call_id, maps:get(call, Second)),
    ok = application:set_env(vault_calls, rooms, #{}),
    ok = vault_calls_monitor:check_now(),
    ok = wait_missing(SecondId, 100),
    ok = gen_server:stop(Monitor).

wait_missing(Id, Attempts) ->
    case vault_calls_store:get_call(vault_calls_store, Id) of
        {error, not_found} -> ok;
        _ when Attempts =:= 0 -> error(call_still_active);
        _ -> timer:sleep(20), wait_missing(Id, Attempts - 1)
    end.

create(Identity, CommandId) ->
    vault_calls_rpc:create(ctx:new(), #{identity => Identity, room_id => <<"room-1">>, command_id => CommandId}).

identity(User, Session) ->
    #{access_token => <<"prototype:", User/binary, ":", Session/binary>>, user_id => User, session_id => Session}.

response_call({ok, #{call := Call}, _}) -> Call.
