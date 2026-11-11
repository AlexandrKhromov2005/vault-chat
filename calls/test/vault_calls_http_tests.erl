-module(vault_calls_http_tests).
-moduledoc "End-to-end tests of the loopback HTTP prototype.".

-include_lib("eunit/include/eunit.hrl").

http_test_() ->
    {foreach, fun setup/0, fun cleanup/1, [
        fun full_call_lifecycle/1,
        fun invalid_requests_are_bounded/1,
        fun dependencies_fail_closed/1
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
         {http_enabled, true}, {auth_delay_ms, 0}, {chat_delay_ms, 0},
         {auth_available, true}, {chat_available, true},
         {rooms, #{<<"room-1">> => [<<"alice">>, <<"bob">>]}}]],
    {ok, _} = application:ensure_all_started(inets),
    {ok, _} = application:ensure_all_started(vault_calls),
    ok.

cleanup(_) ->
    ok = application:stop(vault_calls),
    ok = application:stop(inets),
    ok = application:unload(vault_calls).

full_call_lifecycle(_) ->
    Unauthorized = post(<<"/api/v1/calls">>, undefined, #{<<"room_id">> => <<"room-1">>, <<"command_id">> => <<"c1">>}),
    Body = #{<<"room_id">> => <<"room-1">>, <<"command_id">> => <<"c1">>},
    Created = post(<<"/api/v1/calls">>, <<"alice:phone">>, Body),
    Replay = post(<<"/api/v1/calls">>, <<"alice:phone">>, Body),
    Id = field(Created, <<"call_id">>, <<"missing">>),
    Reused = post(<<"/api/v1/calls">>, <<"alice:phone">>, Body#{<<"note">> => <<"other">>}),
    AcceptBody = #{<<"command_id">> => <<"a1">>},
    Accepted = post(<<"/api/v1/calls/", Id/binary, "/accept">>, <<"bob:laptop">>, AcceptBody),
    AcceptReplay = post(<<"/api/v1/calls/", Id/binary, "/accept">>, <<"bob:laptop">>, AcceptBody),
    WrongRole = post(<<"/api/v1/calls/", Id/binary, "/accept">>, <<"alice:phone">>, AcceptBody),
    FirstConnected = post(<<"/api/v1/calls/", Id/binary, "/connected">>, <<"alice:phone">>, #{<<"command_id">> => <<"c-a">>}),
    Active = post(<<"/api/v1/calls/", Id/binary, "/connected">>, <<"bob:laptop">>, #{<<"command_id">> => <<"c-b">>}),
    Offer = post(<<"/api/v1/calls/", Id/binary, "/signal">>, <<"alice:phone">>,
                 #{<<"kind">> => <<"offer">>, <<"payload">> => <<"sdp">>, <<"sender_seq">> => 1}),
    OfferReplay = post(<<"/api/v1/calls/", Id/binary, "/signal">>, <<"alice:phone">>,
                       #{<<"kind">> => <<"offer">>, <<"payload">> => <<"sdp">>, <<"sender_seq">> => 1}),
    SeqReused = post(<<"/api/v1/calls/", Id/binary, "/signal">>, <<"alice:phone">>,
                     #{<<"kind">> => <<"offer">>, <<"payload">> => <<"other">>, <<"sender_seq">> => 1}),
    Signals = get(<<"/api/v1/calls/", Id/binary, "/signals?after=0">>, <<"bob:laptop">>),
    Heartbeat = post(<<"/api/v1/calls/", Id/binary, "/heartbeat">>, <<"bob:laptop">>, #{<<"command_id">> => <<"h1">>}),
    EndBody = #{<<"command_id">> => <<"e1">>},
    Ended = post(<<"/api/v1/calls/", Id/binary, "/end">>, <<"alice:phone">>, EndBody),
    EndReplay = post(<<"/api/v1/calls/", Id/binary, "/end">>, <<"alice:phone">>, EndBody),
    Missing = get(<<"/api/v1/calls/", Id/binary>>, <<"bob:laptop">>),
    [?_assertEqual(401, status(Unauthorized)),
     ?_assertEqual(201, status(Created)),
     ?_assertEqual(201, status(Replay)),
     ?_assertEqual(field(Created, <<"call_id">>, missing), field(Replay, <<"call_id">>, missing)),
     ?_assertEqual(422, status(Reused)),
     ?_assertEqual({200, <<"connecting">>}, {status(Accepted), field(Accepted, <<"state">>, missing)}),
     ?_assertEqual({200, <<"connecting">>}, {status(AcceptReplay), field(AcceptReplay, <<"state">>, missing)}),
     ?_assertEqual(409, status(WrongRole)),
     ?_assertEqual(200, status(FirstConnected)),
     ?_assertEqual(<<"active">>, field(Active, <<"state">>, missing)),
     ?_assertEqual({200, 1}, {status(Offer), field(Offer, <<"signal_seq">>, missing)}),
     ?_assertEqual({200, 1}, {status(OfferReplay), field(OfferReplay, <<"signal_seq">>, missing)}),
     ?_assertEqual(409, status(SeqReused)),
     ?_assertEqual(200, status(Signals)),
     ?_assertEqual(200, status(Heartbeat)),
     ?_assertEqual(200, status(Ended)),
     ?_assertEqual(200, status(EndReplay)),
     ?_assertEqual(404, status(Missing))].

invalid_requests_are_bounded(_) ->
    UnknownRoom = post(<<"/api/v1/calls">>, <<"alice:phone">>, #{<<"room_id">> => <<"unknown">>, <<"command_id">> => <<"x">>}),
    InvalidJson = raw_post(<<"/api/v1/calls">>, <<"alice:phone">>, <<"{">>),
    Oversized = raw_post(<<"/api/v1/calls">>, <<"alice:phone">>, binary:copy(<<"x">>, 16385)),
    Missing = get(<<"/api/v1/calls/missing">>, <<"alice:phone">>),
    [?_assertEqual(404, status(UnknownRoom)),
     ?_assertEqual(400, status(InvalidJson)),
     ?_assertEqual(413, status(Oversized)),
     ?_assertEqual(404, status(Missing))].

dependencies_fail_closed(_) ->
    ok = application:set_env(vault_calls, auth_available, false),
    AuthDown = post(<<"/api/v1/calls">>, <<"alice:phone">>, #{<<"room_id">> => <<"room-1">>, <<"command_id">> => <<"c1">>}),
    ok = application:set_env(vault_calls, auth_available, true),
    ok = application:set_env(vault_calls, chat_available, false),
    ChatDown = post(<<"/api/v1/calls">>, <<"alice:phone">>, #{<<"room_id">> => <<"room-1">>, <<"command_id">> => <<"c1">>}),
    [?_assertEqual(503, status(AuthDown)),
     ?_assertEqual(503, status(ChatDown))].

post(Path, Session, Body) ->
    raw_post(Path, Session, iolist_to_binary(json:encode(Body))).

raw_post(Path, Session, Body) ->
    request(post, Path, Session, Body).

get(Path, Session) ->
    request(get, Path, Session, <<>>).

request(Method, Path, Session, Body) ->
    Headers = case Session of
        undefined -> [];
        _ -> [{"authorization", "Bearer prototype:" ++ binary_to_list(Session)}]
    end,
    Url = "http://127.0.0.1:" ++ integer_to_list(vault_calls_http:port()) ++ binary_to_list(Path),
    HttpRequest = case Method of
        get -> {Url, Headers};
        _ -> {Url, Headers, "application/json", Body}
    end,
    Result = httpc:request(Method, HttpRequest, [{timeout, 5000}], []),
    case Result of
        {ok, {{_, Status, _}, ResponseHeaders, ResponseBody}} ->
            Decoded = try json:decode(iolist_to_binary(ResponseBody)) of
                Value -> Value
            catch
                _:_ -> ResponseBody
            end,
            case Decoded of
                Map when is_map(Map) -> {Status, ResponseHeaders, Map};
                _ -> {Status, ResponseHeaders, ResponseBody}
            end;
        {error, Reason} -> {0, [], Reason}
    end.

status({Status, _, _}) -> Status.

field({_, _, Body}, Key, Default) when is_map(Body) -> maps:get(Key, Body, Default);
field(_, _, Default) -> Default.
