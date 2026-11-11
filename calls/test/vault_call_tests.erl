-module(vault_call_tests).
-moduledoc "EUnit tests of the call state machine of docs/adr/0005-call-control.md.".

-include_lib("eunit/include/eunit.hrl").

-define(CALLER, <<"alice">>).
-define(CALLEE, <<"bob">>).
-define(PHONE, <<"alice-phone">>).

%% Every test runs the application with timeouts in milliseconds.
calls_test_() ->
    {foreach, fun setup/0, fun cleanup/1, [
        fun accept_takes_one_callee_device/1,
        fun both_devices_connected_make_the_call_active/1,
        fun only_the_right_role_ends_a_ringing_call/1,
        fun ringing_and_connecting_time_out/1,
        fun heartbeats_keep_an_active_call_and_a_missed_one_ends_it/1,
        fun signals_follow_the_matrix/1,
        fun signals_wait_for_the_previous_answer/1,
        fun simultaneous_accepts_have_one_winner/1,
        fun a_crash_ends_only_its_own_call/1,
        fun malformed_commands_do_not_crash_the_call/1
    ]}.

setup() ->
    ok = application:load(vault_calls),
    [ok = application:set_env(vault_calls, Key, Value) || {Key, Value} <-
        [{ringing_timeout, 300}, {connecting_timeout, 300}, {heartbeat_timeout, 150}, {max_call_duration, 5000}]],
    {ok, _} = application:ensure_all_started(vault_calls),
    ok.

cleanup(_) ->
    ok = application:stop(vault_calls),
    ok = application:unload(vault_calls).

call(Id) ->
    {ok, Pid} = vault_call_sup:start_call(#{id => Id, caller => ?CALLER, caller_device => ?PHONE, callee => ?CALLEE}),
    Pid.

cmd(Id, Name, User, Device) -> vault_call:command(Id, {Name, User, Device}).
sig(Id, User, Device, Kind) -> vault_call:command(Id, {signal, User, Device, Kind}).

connect(Id) ->
    {ok, connecting} = cmd(Id, accept, ?CALLEE, <<"bob-laptop">>),
    {ok, connecting} = cmd(Id, connected, ?CALLER, ?PHONE),
    {ok, active} = cmd(Id, connected, ?CALLEE, <<"bob-laptop">>).

accept_takes_one_callee_device(_) ->
    call(<<"c1">>),
    [?_assertEqual({error, not_found}, cmd(<<"c1">>, accept, <<"eve">>, <<"eve-phone">>)),
     ?_assertEqual({error, conflict}, cmd(<<"c1">>, accept, ?CALLER, ?PHONE)),
     ?_assertEqual({ok, connecting}, cmd(<<"c1">>, accept, ?CALLEE, <<"bob-laptop">>)),
     ?_assertEqual({error, conflict}, cmd(<<"c1">>, accept, ?CALLEE, <<"bob-phone">>)),
     ?_assertEqual({error, conflict}, cmd(<<"c1">>, heartbeat, ?CALLEE, <<"bob-phone">>)),
     ?_assertEqual({error, conflict}, cmd(<<"c1">>, connected, ?CALLER, <<"alice-laptop">>))].

both_devices_connected_make_the_call_active(_) ->
    call(<<"c2">>),
    {ok, connecting} = cmd(<<"c2">>, accept, ?CALLEE, <<"bob-laptop">>),
    First = cmd(<<"c2">>, connected, ?CALLER, ?PHONE),
    Repeat = cmd(<<"c2">>, connected, ?CALLER, ?PHONE),
    Second = cmd(<<"c2">>, connected, ?CALLEE, <<"bob-laptop">>),
    [?_assertEqual({ok, connecting}, First), ?_assertEqual({ok, connecting}, Repeat),
     ?_assertEqual({ok, active}, Second), ?_assertEqual({ok, active}, vault_call:state(<<"c2">>))].

only_the_right_role_ends_a_ringing_call(_) ->
    [call(Id) || Id <- [<<"c3">>, <<"c4">>]],
    [?_assertEqual({error, conflict}, cmd(<<"c3">>, cancel, ?CALLEE, <<"bob-laptop">>)),
     ?_assertEqual({error, conflict}, cmd(<<"c3">>, reject, ?CALLER, ?PHONE)),
     ?_assertEqual({error, conflict}, cmd(<<"c3">>, cancel, ?CALLER, <<"alice-laptop">>)),
     ?_assertEqual({ok, ended}, cmd(<<"c3">>, cancel, ?CALLER, ?PHONE)),
     ?_assertEqual({error, not_found}, cmd(<<"c3">>, accept, ?CALLEE, <<"bob-laptop">>)),
     ?_assertEqual({ok, ended}, cmd(<<"c4">>, reject, ?CALLEE, <<"bob-phone">>)),
     ?_assertEqual({error, not_found}, vault_call:state(<<"c4">>))].

ringing_and_connecting_time_out(_) ->
    call(<<"c5">>),
    call(<<"c6">>),
    {ok, connecting} = cmd(<<"c6">>, accept, ?CALLEE, <<"bob-laptop">>),
    timer:sleep(450),
    [?_assertEqual({error, not_found}, vault_call:state(<<"c5">>)),
     ?_assertEqual({error, not_found}, vault_call:state(<<"c6">>))].

heartbeats_keep_an_active_call_and_a_missed_one_ends_it(_) ->
    call(<<"c7">>),
    connect(<<"c7">>),
    Beat = fun(Devices) -> [cmd(<<"c7">>, heartbeat, U, D) || {U, D} <- Devices] end,
    Both = [{?CALLER, ?PHONE}, {?CALLEE, <<"bob-laptop">>}],
    Alive = [begin timer:sleep(60), Beat(Both) end || _ <- lists:seq(1, 6)],
    StillActive = vault_call:state(<<"c7">>),
    _ = [begin timer:sleep(60), Beat([{?CALLER, ?PHONE}]) end || _ <- lists:seq(1, 4)],
    [?_assert(lists:all(fun(R) -> R =:= {ok, active} end, lists:append(Alive))),
     ?_assertEqual({ok, active}, StillActive),
     ?_assertEqual({error, not_found}, vault_call:state(<<"c7">>))].

signals_follow_the_matrix(_) ->
    call(<<"c8">>),
    Ringing = sig(<<"c8">>, ?CALLER, ?PHONE, offer),
    {ok, connecting} = cmd(<<"c8">>, accept, ?CALLEE, <<"bob-laptop">>),
    Early = [sig(<<"c8">>, ?CALLEE, <<"bob-laptop">>, answer), sig(<<"c8">>, ?CALLER, ?PHONE, candidate),
             sig(<<"c8">>, ?CALLEE, <<"bob-laptop">>, offer), sig(<<"c8">>, ?CALLER, ?PHONE, answer)],
    Ordered = [sig(<<"c8">>, ?CALLER, ?PHONE, offer), sig(<<"c8">>, ?CALLER, ?PHONE, candidate),
               sig(<<"c8">>, ?CALLEE, <<"bob-laptop">>, answer), sig(<<"c8">>, ?CALLEE, <<"bob-laptop">>, end_of_candidates)],
    Other = sig(<<"c8">>, ?CALLEE, <<"bob-phone">>, candidate),
    Stranger = sig(<<"c8">>, <<"eve">>, <<"eve-phone">>, candidate),
    {ok, _} = cmd(<<"c8">>, connected, ?CALLER, ?PHONE),
    {ok, active} = cmd(<<"c8">>, connected, ?CALLEE, <<"bob-laptop">>),
    Active = [sig(<<"c8">>, ?CALLEE, <<"bob-laptop">>, offer), sig(<<"c8">>, ?CALLER, ?PHONE, answer),
              sig(<<"c8">>, ?CALLER, ?PHONE, candidate), sig(<<"c8">>, ?CALLER, ?PHONE, end_of_candidates)],
    [?_assertEqual({error, conflict}, Ringing),
     ?_assertEqual(lists:duplicate(4, {error, conflict}), Early),
     ?_assertEqual(lists:duplicate(4, {ok, connecting}), Ordered),
     ?_assertEqual({error, conflict}, Other),
     ?_assertEqual({error, not_found}, Stranger),
     ?_assertEqual([{ok, active}, {ok, active}, {ok, active}, {error, conflict}], Active)].

signals_wait_for_the_previous_answer(_) ->
    call(<<"c13">>),
    {ok, connecting} = cmd(<<"c13">>, accept, ?CALLEE, <<"bob-laptop">>),
    Connecting = [sig(<<"c13">>, ?CALLER, ?PHONE, offer),
                  sig(<<"c13">>, ?CALLER, ?PHONE, offer),
                  sig(<<"c13">>, ?CALLEE, <<"bob-laptop">>, answer),
                  sig(<<"c13">>, ?CALLEE, <<"bob-laptop">>, answer),
                  sig(<<"c13">>, ?CALLER, ?PHONE, offer),
                  sig(<<"c13">>, ?CALLEE, <<"bob-laptop">>, answer)],
    {ok, connecting} = cmd(<<"c13">>, connected, ?CALLER, ?PHONE),
    {ok, active} = cmd(<<"c13">>, connected, ?CALLEE, <<"bob-laptop">>),
    Active = [sig(<<"c13">>, ?CALLEE, <<"bob-laptop">>, offer),
              sig(<<"c13">>, ?CALLEE, <<"bob-laptop">>, offer),
              sig(<<"c13">>, ?CALLER, ?PHONE, answer),
              sig(<<"c13">>, ?CALLER, ?PHONE, answer),
              sig(<<"c13">>, ?CALLER, ?PHONE, offer),
              sig(<<"c13">>, ?CALLEE, <<"bob-laptop">>, answer)],
    [?_assertEqual([{ok, connecting}, {error, conflict}, {ok, connecting},
                    {error, conflict}, {ok, connecting}, {ok, connecting}], Connecting),
     ?_assertEqual([{ok, active}, {error, conflict}, {ok, active},
                    {error, conflict}, {ok, active}, {ok, active}], Active)].

simultaneous_accepts_have_one_winner(_) ->
    call(<<"c9">>),
    Self = self(),
    Devices = [<<"bob-", (integer_to_binary(N))/binary>> || N <- lists:seq(1, 8)],
    [spawn(fun() -> Self ! {accepted, cmd(<<"c9">>, accept, ?CALLEE, D)} end) || D <- Devices],
    Results = [receive {accepted, R} -> R after 2000 -> timeout end || _ <- Devices],
    [?_assertEqual(1, length([R || {ok, connecting} = R <- Results])),
     ?_assertEqual(7, length([R || {error, conflict} = R <- Results]))].

a_crash_ends_only_its_own_call(_) ->
    Crashed = call(<<"c10">>),
    call(<<"c11">>),
    connect(<<"c11">>),
    Ref = monitor(process, Crashed),
    exit(Crashed, kill),
    receive {'DOWN', Ref, process, Crashed, killed} -> ok after 1000 -> error(not_killed) end,
    timer:sleep(20),
    [?_assertEqual({error, not_found}, vault_call:state(<<"c10">>)),
     ?_assertEqual({ok, active}, vault_call:state(<<"c11">>)),
     ?_assertEqual({ok, active}, cmd(<<"c11">>, heartbeat, ?CALLER, ?PHONE))].

malformed_commands_do_not_crash_the_call(_) ->
    call(<<"c12">>),
    [?_assertEqual({error, badarg}, vault_call:command(<<"c12">>, garbage)),
     ?_assertEqual({error, badarg}, vault_call:command(<<"c12">>, {accept})),
     ?_assertEqual({error, conflict}, vault_call:command(<<"c12">>, {launch, ?CALLER, ?PHONE})),
     ?_assertEqual({ok, ringing}, vault_call:state(<<"c12">>))].
