-module(vault_calls_events_tests).

-include_lib("eunit/include/eunit.hrl").

publishes_committed_changes_to_participants_test() ->
    Parent = self(),
    {ok, _} = vault_calls_events:start_link(#{publisher => fun(Channel, Payload) ->
        Parent ! {published, Channel, Payload}, ok
    end}),
    ok = vault_calls_events:call_changed(call(ended)),
    ok = vault_calls_events:signal(call(connecting), #{signal_seq => 2, sender_seq => 1,
                                                       kind => offer, payload => <<"sdp">>}),
    ?assertEqual(<<"chat:user:alice">>, channel()),
    ?assertEqual(<<"chat:user:bob">>, channel()),
    ?assertEqual(<<"chat:user:alice">>, channel()),
    ?assertEqual(<<"chat:user:bob">>, channel()),
    ?assertEqual({4, 0, 0}, vault_calls_events:counts()),
    ok = vault_calls_events:stop().

new_call_wakes_callee_without_signaling_content_test() ->
    Parent = self(),
    {ok, _} = vault_calls_events:start_link(#{publisher => fun(Channel, Payload) ->
        Parent ! {published, Channel, Payload}, ok
    end}),
    ok = vault_calls_events:call_changed(call(ringing)),
    ?assertEqual(<<"chat:user:alice">>, channel()),
    ?assertEqual(<<"chat:user:bob">>, channel()),
    receive
        {published, <<"push:signal:v1">>, Payload} ->
            ?assertEqual(#{<<"room_id">> => <<"room-1">>, <<"user_ids">> => [<<"bob">>]}, json:decode(Payload))
    after 1000 -> error(missing_push_signal)
    end,
    ok = vault_calls_events:stop().

queue_is_bounded_test() ->
    Parent = self(),
    {ok, _} = vault_calls_events:start_link(#{publisher => fun(_, _) ->
        case get(publisher_blocked) of
            undefined ->
                put(publisher_blocked, true),
                Parent ! publisher_blocked,
                receive release -> ok end;
            _ -> ok
        end
    end}),
    [ok = vault_calls_events:call_changed(call(ended, integer_to_binary(N))) || N <- lists:seq(1, 600)],
    receive publisher_blocked -> ok after 1000 -> error(publisher_not_started) end,
    vault_calls_events ! release,
    {_Published, Dropped, Queued} = vault_calls_events:counts(),
    ?assert(Dropped > 0),
    ?assert(Queued =< 1024),
    ok = vault_calls_events:stop().

call(State) -> call(State, <<"call-1">>).
call(State, Id) ->
    #{id => Id, room_id => <<"room-1">>, state => State, state_version => 2,
      caller => <<"alice">>, callee => <<"bob">>}.

channel() ->
    receive {published, Channel, _Payload} -> Channel
    after 1000 -> error(missing_publication)
    end.
