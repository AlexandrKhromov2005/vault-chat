-module(vault_calls_store_tests).
-moduledoc "PostgreSQL admission and command-journal acceptance tests.".

-include_lib("eunit/include/eunit.hrl").

store_test_() ->
    {foreach, fun setup/0, fun cleanup/1, [
        fun create_replays_its_original_response/1,
        fun concurrent_replays_share_one_response/1,
        fun concurrent_participants_admit_one_call/1,
        fun the_two_hundredth_call_is_the_last_one/1,
        fun malformed_creation_is_atomic/1
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
    #{url => Url, store => Store, connection => Connection}.

cleanup(#{store := Store, connection := Connection}) ->
    ok = vault_calls_store:stop(Store),
    ok = epgsql:close(Connection),
    ok = application:unload(vault_calls).

create_replays_its_original_response(#{store := Store, connection := Connection}) ->
    Args = args(<<"create-1">>, <<"alice">>, <<"bob">>),
    First = vault_calls_store:create_call(Store, Args),
    Replay = vault_calls_store:create_call(Store, Args),
    Reused = vault_calls_store:create_call(Store, Args#{fingerprint := <<1:256>>}),
    {ok, _, [{Calls}]} = epgsql:squery(Connection, "SELECT count(*) FROM calls"),
    {ok, _, [{Active}]} = epgsql:squery(Connection, "SELECT active_calls FROM call_capacity WHERE id = 1"),
    [?_assertMatch({ok, #{<<"call_id">> := <<"create-1">>, <<"state">> := <<"ringing">>}}, First),
     ?_assertEqual(First, Replay),
     ?_assertEqual({error, command_id_reused}, Reused),
     ?_assertEqual(1, binary_to_integer(Calls)),
     ?_assertEqual(1, binary_to_integer(Active))].

concurrent_replays_share_one_response(#{url := Url, connection := Connection}) ->
    {ok, Store1} = vault_calls_store:start_link(Url),
    {ok, Store2} = vault_calls_store:start_link(Url),
    Args = args(<<"concurrent-replay">>, <<"alice">>, <<"bob">>),
    [First, Second] = concurrent_create([{Store1, Args}, {Store2, Args}]),
    ok = vault_calls_store:stop(Store1),
    ok = vault_calls_store:stop(Store2),
    {ok, _, [{Calls}]} = epgsql:squery(Connection, "SELECT count(*) FROM calls"),
    [?_assertMatch({ok, _}, First),
     ?_assertEqual(First, Second),
     ?_assertEqual(1, binary_to_integer(Calls))].

malformed_creation_is_atomic(#{store := Store, connection := Connection}) ->
    Invalid = (args(<<"invalid">>, <<"alice">>, <<"bob">>))#{callee := <<"alice">>},
    Reply = vault_calls_store:create_call(Store, Invalid),
    StillValid = vault_calls_store:create_call(Store, args(<<"valid">>, <<"alice">>, <<"bob">>)),
    {ok, _, [{Commands}]} = epgsql:squery(Connection, "SELECT count(*) FROM call_commands"),
    [?_assertEqual({error, badarg}, Reply),
     ?_assertMatch({ok, _}, StillValid),
     ?_assertEqual(1, binary_to_integer(Commands))].

concurrent_participants_admit_one_call(#{url := Url}) ->
    Stores = [begin {ok, Store} = vault_calls_store:start_link(Url), Store end || _ <- lists:seq(1, 3)],
    Requests = [{hd(Stores), args(<<"shared-caller">>, <<"alice">>, <<"bob">>)},
                {lists:nth(2, Stores), args(<<"other-callee">>, <<"alice">>, <<"carol">>)},
                {lists:nth(3, Stores), args(<<"reverse">>, <<"bob">>, <<"alice">>)}],
    Results = concurrent_create(Requests),
    [ok = vault_calls_store:stop(Each) || Each <- Stores],
    [?_assertEqual(1, length([ok || {ok, _} <- Results])),
     ?_assertEqual(2, length([busy || {error, busy} <- Results]))].

the_two_hundredth_call_is_the_last_one(#{url := Url, connection := Connection}) ->
    {ok, 199} = epgsql:squery(Connection,
        "INSERT INTO calls (id, room_id, caller_id, caller_device, callee_id) "
        "SELECT 'seed-' || g, 'room-' || g, 'seed-a-' || g, 'device-a-' || g, 'seed-b-' || g "
        "FROM generate_series(1, 199) AS g"),
    {ok, 398} = epgsql:squery(Connection,
        "INSERT INTO call_participants (user_id, call_id) "
        "SELECT 'seed-a-' || g, 'seed-' || g FROM generate_series(1, 199) AS g "
        "UNION ALL SELECT 'seed-b-' || g, 'seed-' || g FROM generate_series(1, 199) AS g"),
    {ok, 1} = epgsql:squery(Connection, "UPDATE call_capacity SET active_calls = 199 WHERE id = 1"),
    {ok, Store1} = vault_calls_store:start_link(Url),
    {ok, Store2} = vault_calls_store:start_link(Url),
    Results = concurrent_create([{Store1, args(<<"call-200">>, <<"alice200">>, <<"bob200">>)},
                                 {Store2, args(<<"call-201">>, <<"alice201">>, <<"bob201">>)}]),
    ok = vault_calls_store:stop(Store1),
    ok = vault_calls_store:stop(Store2),
    {ok, _, [{Active}]} = epgsql:squery(Connection, "SELECT active_calls FROM call_capacity WHERE id = 1"),
    [?_assertEqual(1, length([ok || {ok, _} <- Results])),
     ?_assertEqual(1, length([capacity || {error, capacity} <- Results])),
     ?_assertEqual(200, binary_to_integer(Active))].

concurrent_create(Requests) ->
    Self = self(),
    Tagged = [{make_ref(), Store, Args} || {Store, Args} <- Requests],
    [spawn(fun() -> Self ! {Ref, vault_calls_store:create_call(Store, Args)} end)
     || {Ref, Store, Args} <- Tagged],
    [receive {Ref, Result} -> Result after 3000 -> error(create_timeout) end || {Ref, _, _} <- Tagged].

args(Id, Caller, Callee) ->
    #{id => Id, room_id => <<"room-", Id/binary>>, caller => Caller,
      caller_device => <<Caller/binary, "-phone">>, callee => Callee,
      command_id => <<"command-", Id/binary>>, fingerprint => crypto:hash(sha256, Id)}.
