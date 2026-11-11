-module(vault_call_recovery_tests).
-moduledoc "Database-backed lifecycle and recovery acceptance tests.".

-include_lib("eunit/include/eunit.hrl").

recovery_test_() ->
    {foreach, fun setup/0, fun cleanup/1, [
        fun creation_is_persistent/1,
        fun ending_releases_participants_and_capacity/1,
        fun a_crashed_process_recovers_active_state/1,
        fun an_application_restart_recovers_active_state/1,
        fun an_expired_call_ends_during_startup/1,
        fun a_database_outage_blocks_the_transition/1,
        fun client_commands_replay_their_original_response/1
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
    ok = vault_calls_store:stop(Store),
    [ok = application:set_env(vault_calls, Key, Value) || {Key, Value} <-
        [{database_url, Url}, {ringing_timeout, 300}, {connecting_timeout, 300},
         {heartbeat_timeout, 150}, {max_call_duration, 5000}]],
    {ok, _} = application:ensure_all_started(vault_calls),
    #{connection => Connection}.

cleanup(#{connection := Connection}) ->
    ok = application:stop(vault_calls),
    ok = epgsql:close(Connection),
    ok = application:unload(vault_calls).

creation_is_persistent(#{connection := Connection}) ->
    _ = new_call(<<"persistent">>),
    [?_assertEqual({<<"ringing">>, 0, 1}, database_state(Connection, <<"persistent">>))].

ending_releases_participants_and_capacity(#{connection := Connection}) ->
    _ = new_call(<<"ending">>),
    Ended = vault_call:command(<<"ending">>, {cancel, <<"alice">>, <<"alice-phone">>}),
    [?_assertEqual({ok, ended}, Ended),
     ?_assertEqual({<<"ended">>, 1, 0}, database_state(Connection, <<"ending">>)),
     ?_assertEqual(0, participant_count(Connection))].

a_crashed_process_recovers_active_state(_) ->
    Pid = new_call(<<"crash">>),
    ok = connect(<<"crash">>),
    Ref = monitor(process, Pid),
    exit(Pid, kill),
    receive {'DOWN', Ref, process, Pid, killed} -> ok after 1000 -> error(not_killed) end,
    timer:sleep(50),
    [?_assertEqual({ok, active}, vault_call:state(<<"crash">>))].

an_application_restart_recovers_active_state(_) ->
    _ = new_call(<<"restart">>),
    ok = connect(<<"restart">>),
    ok = application:stop(vault_calls),
    {ok, _} = application:ensure_all_started(vault_calls),
    [?_assertEqual({ok, active}, vault_call:state(<<"restart">>))].

an_expired_call_ends_during_startup(#{connection := Connection}) ->
    _ = new_call(<<"expired">>),
    ok = application:stop(vault_calls),
    timer:sleep(450),
    {ok, _} = application:ensure_all_started(vault_calls),
    timer:sleep(50),
    [?_assertEqual({error, not_found}, vault_call:state(<<"expired">>)),
     ?_assertEqual({<<"ended">>, 1, 0}, database_state(Connection, <<"expired">>)),
     ?_assertEqual(0, participant_count(Connection))].

a_database_outage_blocks_the_transition(#{connection := Connection}) ->
    _ = new_call(<<"blocked">>),
    Database = sys:get_state(vault_calls_store),
    ok = epgsql:close(Database),
    Reply = vault_call:command(<<"blocked">>, {accept, <<"bob">>, <<"bob-laptop">>}),
    [?_assertEqual({error, unavailable}, Reply),
     ?_assertEqual({ok, ringing}, vault_call:state(<<"blocked">>)),
     ?_assertEqual({<<"ringing">>, 0, 1}, database_state(Connection, <<"blocked">>))].

client_commands_replay_their_original_response(_) ->
    _ = new_call(<<"replay">>),
    Accept = {accept, <<"bob">>, <<"bob-laptop">>},
    AcceptMeta = command_meta(<<"replay-accept">>, <<"bob-laptop">>),
    First = vault_call:command(<<"replay">>, Accept, AcceptMeta),
    {ok, connecting} = vault_call:command(<<"replay">>, {connected, <<"alice">>, <<"alice-phone">>}),
    {ok, active} = vault_call:command(<<"replay">>, {connected, <<"bob">>, <<"bob-laptop">>}),
    Replay = vault_call:command(<<"replay">>, Accept, AcceptMeta),
    Reused = vault_call:command(<<"replay">>, Accept, AcceptMeta#{fingerprint := <<1:256>>}),
    EndCommand = {'end', <<"alice">>, <<"alice-phone">>},
    EndMeta = command_meta(<<"replay-end">>, <<"alice-phone">>),
    Ended = vault_call:command(<<"replay">>, EndCommand, EndMeta),
    EndReplay = vault_call:command(<<"replay">>, EndCommand, EndMeta),
    [?_assertEqual({ok, connecting}, First),
     ?_assertEqual(First, Replay),
     ?_assertEqual({error, command_id_reused}, Reused),
     ?_assertEqual({ok, ended}, Ended),
     ?_assertEqual(Ended, EndReplay)].

command_meta(Id, Device) ->
    #{device => Device, command_id => Id, fingerprint => crypto:hash(sha256, Id)}.

new_call(Id) ->
    {ok, Pid} = vault_call_sup:start_call(#{id => Id, room_id => <<"room-", Id/binary>>,
        caller => <<"alice">>, caller_device => <<"alice-phone">>, callee => <<"bob">>,
        command_id => <<"create-", Id/binary>>, fingerprint => crypto:hash(sha256, Id)}),
    Pid.

connect(Id) ->
    {ok, connecting} = vault_call:command(Id, {accept, <<"bob">>, <<"bob-laptop">>}),
    {ok, connecting} = vault_call:command(Id, {connected, <<"alice">>, <<"alice-phone">>}),
    {ok, active} = vault_call:command(Id, {connected, <<"bob">>, <<"bob-laptop">>}),
    ok.

database_state(Connection, Id) ->
    {ok, _, Rows} = epgsql:equery(Connection,
        "SELECT state, state_version, (SELECT active_calls FROM call_capacity WHERE id = 1) "
        "FROM calls WHERE id = $1", [Id]),
    case Rows of
        [{State, Version, Active}] -> {State, Version, Active};
        [] -> missing
    end.

participant_count(Connection) ->
    {ok, _, [{Count}]} = epgsql:squery(Connection, "SELECT count(*) FROM call_participants"),
    binary_to_integer(Count).
