-module(vault_calls_loadgen).
-moduledoc "Measured 200-call acceptance and restart drill for the HTTP prototype.".

-export([setup/1, verify/1, percentile/2]).

-spec setup(file:name_all()) -> no_return().
setup(Directory) ->
    Count = count(),
    Rooms = rooms(Count),
    BaselineRss = rss_bytes(),
    RssMonitor = start_rss_monitor(),
    ok = start(Rooms, true),
    ok = warmup(),
    Started = erlang:monotonic_time(microsecond),
    _ = erlang:system_flag(scheduler_wall_time, true),
    SchedulerStarted = erlang:statistics(scheduler_wall_time),
    Results = concurrently(Count),
    Elapsed = erlang:monotonic_time(microsecond) - Started,
    SchedulerEnded = erlang:statistics(scheduler_wall_time),
    Latencies = lists:append([Times || {ok, _, Times} <- Results]),
    Errors = length([Error || {error, _} = Error <- Results]),
    {ok, Ids} = vault_calls_store:list_active(vault_calls_store),
    case length(Ids) =:= Count of
        true -> ok;
        false -> error({active_count_mismatch, Results, Ids})
    end,
    Sample = case [Id || {ok, Id, _} <- Results] of
        [First | _] -> First;
        [] -> error({no_successful_lifecycle, Results})
    end,
    true = crash_recovers(Sample),
    PeakRss = stop_rss_monitor(RssMonitor),
    Metrics = metrics(Count, Latencies, Errors, Elapsed, SchedulerStarted, SchedulerEnded, BaselineRss, PeakRss, Results),
    Metrics2 = Metrics#{<<"process_crash_recovered">> => true, <<"sample_call_id">> => Sample},
    ok = file:write_file(filename:join(Directory, "metrics.json"), json:encode(Metrics2)),
    _ = os:cmd("kill -9 " ++ os:getpid()),
    ok.

-spec verify(file:name_all()) -> ok.
verify(Directory) ->
    {ok, Metrics} = file:read_file(filename:join(Directory, "metrics.json")),
    ok = start(rooms(count()), false),
    {ok, Ids} = vault_calls_store:list_active(vault_calls_store),
    true = length(Ids) =:= count(),
    {ok, active} = vault_call:state(maps:get(<<"sample_call_id">>, json:decode(Metrics))),
    Result = json:decode(Metrics),
    ok = application:stop(vault_calls),
    io:format("~s~n", [json:encode(Result#{<<"restart_recovered">> => true})]),
    case gates(Result) of
        true -> ok;
        false -> error({gate_failed, Result})
    end.

-spec percentile([non_neg_integer()], pos_integer()) -> non_neg_integer().
percentile([], _Percent) -> 0;
percentile(Values, Percent) when Percent > 0, Percent =< 100 ->
    Sorted = lists:sort(Values),
    Index = max(1, (length(Sorted) * Percent + 99) div 100),
    lists:nth(Index, Sorted).

-spec start(map(), boolean()) -> ok.
start(Rooms, Reset) ->
    ok = application:load(vault_calls),
    Url = case os:getenv("VAULT_CHAT_TEST_CALLS_DATABASE_URL") of
        false -> error(database_url_required);
        Value -> unicode:characters_to_binary(Value)
    end,
    Reset andalso reset(Url),
    [ok = application:set_env(vault_calls, Key, Value) || {Key, Value} <-
        [{database_url, Url}, {ringing_timeout, 60000}, {connecting_timeout, 60000},
         {heartbeat_timeout, 60000}, {max_call_duration, 600000}, {http_enabled, true},
         {auth_delay_ms, 0}, {chat_delay_ms, 0}, {auth_available, true},
         {chat_available, true}, {rooms, Rooms}]],
    {ok, _} = application:ensure_all_started(vault_calls),
    ok.

-spec reset(binary()) -> ok.
reset(Url) ->
    {ok, Store} = vault_calls_store:start_link(Url),
    ok = vault_calls_store:migrate(Store),
    {ok, Connection} = vault_calls_store:connect(Url),
    [{ok, [], []}, {ok, 1}] = epgsql:squery(Connection,
        "TRUNCATE call_commands, call_signals, call_participants, calls; UPDATE call_capacity SET active_calls = 0"),
    ok = epgsql:close(Connection),
    ok = vault_calls_store:stop(Store),
    ok.

-spec count() -> pos_integer().
count() ->
    case os:getenv("VAULT_CALLS_LOADGEN_COUNT", "200") of
        Value -> max(1, list_to_integer(Value))
    end.

-spec rooms(pos_integer()) -> map().
rooms(Count) ->
    maps:from_list([{room(N), [user(a, N), user(b, N)]} || N <- lists:seq(0, Count)]).

-spec warmup() -> ok.
warmup() ->
    {ok, CallId, _} = lifecycle(0),
    {_, _} = step(200, path(CallId, "end"), token(a, 0), #{<<"command_id">> => <<"warmup-end">>}),
    ok.

-spec concurrently(pos_integer()) -> list().
concurrently(Count) ->
    lists:append([concurrent_batch(Ns) || Ns <- batches(lists:seq(1, Count), 8)]).

-spec batches([pos_integer()], pos_integer()) -> [[pos_integer()]].
batches([], _Size) -> [];
batches(Values, Size) when length(Values) =< Size -> [Values];
batches(Values, Size) ->
    {Batch, Rest} = lists:split(Size, Values),
    [Batch | batches(Rest, Size)].

-spec concurrent_batch([pos_integer()]) -> list().
concurrent_batch(Ns) ->
    Parent = self(),
    Tagged = [{make_ref(), N} || N <- Ns],
    [spawn(fun() -> Parent ! {Ref, lifecycle(N)} end) || {Ref, N} <- Tagged],
    [receive {Ref, Result} -> Result after 60000 -> error({timeout, N}) end || {Ref, N} <- Tagged].

-spec lifecycle(non_neg_integer()) -> {ok, binary(), [non_neg_integer()]} | {error, term()}.
lifecycle(N) ->
    try
        Caller = token(a, N),
        Callee = token(b, N),
        {Created, T1} = step(201, <<"/api/v1/calls">>, Caller,
            #{<<"room_id">> => room(N), <<"command_id">> => id(<<"create">>, N)}),
        CallId = maps:get(<<"call_id">>, Created),
        {_, T2} = step(200, path(CallId, "accept"), Callee, #{<<"command_id">> => id(<<"accept">>, N)}),
        {_, T3} = step(200, path(CallId, "connected"), Caller, #{<<"command_id">> => id(<<"connected-a">>, N)}),
        {_, T4} = step(200, path(CallId, "connected"), Callee, #{<<"command_id">> => id(<<"connected-b">>, N)}),
        {_, T5} = step(200, path(CallId, "signal"), Caller,
            #{<<"kind">> => <<"offer">>, <<"payload">> => <<"sdp-a">>, <<"sender_seq">> => 1}),
        {_, T6} = step(200, path(CallId, "signal"), Callee,
            #{<<"kind">> => <<"answer">>, <<"payload">> => <<"sdp-b">>, <<"sender_seq">> => 1}),
        {_, T7} = step(200, path(CallId, "heartbeat"), Caller, #{<<"command_id">> => id(<<"heartbeat-a">>, N)}),
        {_, T8} = step(200, path(CallId, "heartbeat"), Callee, #{<<"command_id">> => id(<<"heartbeat-b">>, N)}),
        {ok, CallId, [T1, T2, T3, T4, T5, T6, T7, T8]}
    catch
        _:Reason -> {error, {N, Reason}}
    end.

-spec step(non_neg_integer(), binary(), binary(), map()) -> {map(), non_neg_integer()}.
step(Expected, Path, Session, Body) ->
    {Micros, Status, Response} = timed_post(Path, Session, Body),
    case Status of
        Expected -> {Response, Micros};
        _ -> error({unexpected_status, Expected, Status, Response})
    end.

-spec timed_post(binary(), binary(), map()) -> {non_neg_integer(), non_neg_integer(), map()}.
timed_post(Path, Session, Body) ->
    Url = "http://127.0.0.1:" ++ integer_to_list(vault_calls_http:port()) ++ binary_to_list(Path),
    Json = iolist_to_binary(json:encode(Body)),
    {Micros, Result} = timer:tc(fun() ->
        httpc:request(post, {Url, [{"authorization", "Bearer prototype:" ++ binary_to_list(Session)}],
            "application/json", Json}, [{timeout, 10000}], [])
    end),
    case Result of
        {ok, {{_, Status, _}, _, ResponseBody}} -> {Micros, Status, json:decode(iolist_to_binary(ResponseBody))};
        {error, Reason} -> error({Path, Reason})
    end.

-spec crash_recovers(binary()) -> true.
crash_recovers(Id) ->
    Pid = vault_calls_registry:whereis_name(Id),
    true = is_pid(Pid),
    exit(Pid, kill),
    timer:sleep(100),
    case vault_call:state(Id) of
        {ok, active} -> true;
        Other -> error({crash_not_recovered, Id, Other})
    end.

-spec step_p95(list()) -> map().
step_p95(Results) ->
    Names = [create, accept, connected_a, connected_b, offer, answer, heartbeat_a, heartbeat_b],
    maps:from_list([{Name, percentile([lists:nth(Index, Times) || {ok, _, Times} <- Results], 95)}
                    || {Index, Name} <- lists:zip(lists:seq(1, length(Names)), Names)]).

-spec metrics(pos_integer(), [non_neg_integer()], non_neg_integer(), non_neg_integer(), term(), term(),
              non_neg_integer(), non_neg_integer(), list()) -> map().
metrics(Count, Latencies, Errors, Elapsed, SchedulerStarted, SchedulerEnded, BaselineRss, PeakRss, Results) ->
    Seconds = max(Elapsed / 1000000, 0.000001),
    Delta = max(0, PeakRss - BaselineRss),
    #{<<"calls">> => Count, <<"commands">> => length(Latencies), <<"errors">> => Errors,
      <<"duration_us">> => Elapsed, <<"commands_per_second">> => length(Latencies) / Seconds,
      <<"p95_us">> => percentile(Latencies, 95), <<"p99_us">> => percentile(Latencies, 99),
      <<"scheduler_utilization">> => utilization(SchedulerStarted, SchedulerEnded),
      <<"baseline_rss_bytes">> => BaselineRss, <<"rss_bytes">> => PeakRss, <<"rss_delta_bytes">> => Delta,
      <<"step_p95">> => step_p95(Results),
      <<"gate_status">> => gate_status(Latencies, Errors, BaselineRss, Delta)}.

-spec utilization(term(), term()) -> float().
utilization(undefined, _SchedulerEnded) -> 0.0;
utilization(_SchedulerStarted, undefined) -> 0.0;
utilization(SchedulerStarted, SchedulerEnded) ->
    TotalStart = lists:sum([Total || {_, Total, _} <- SchedulerStarted]),
    ActiveStart = lists:sum([Active || {_, _, Active} <- SchedulerStarted]),
    TotalEnd = lists:sum([Total || {_, Total, _} <- SchedulerEnded]),
    ActiveEnd = lists:sum([Active || {_, _, Active} <- SchedulerEnded]),
    Total = TotalEnd - TotalStart,
    case Total > 0 of
        true -> min(1.0, (ActiveEnd - ActiveStart) / 1000 / Total);
        false -> 0.0
    end.

-spec start_rss_monitor() -> {pid(), reference()}.
start_rss_monitor() ->
    Parent = self(),
    Ref = make_ref(),
    {spawn(fun() -> rss_monitor(Parent, Ref, 0) end), Ref}.

-spec rss_monitor(pid(), reference(), non_neg_integer()) -> ok.
rss_monitor(Parent, Ref, Peak) ->
    receive
        {Ref, stop} ->
            Parent ! {Ref, peak, max(Peak, rss_bytes())},
            ok
    after 20 ->
        rss_monitor(Parent, Ref, max(Peak, rss_bytes()))
    end.

-spec stop_rss_monitor({pid(), reference()}) -> non_neg_integer().
stop_rss_monitor({Pid, Ref}) ->
    Pid ! {Ref, stop},
    receive {Ref, peak, Peak} -> Peak after 5000 -> error(rss_monitor_timeout) end.

-spec rss_bytes() -> non_neg_integer().
rss_bytes() ->
    list_to_integer(string:trim(os:cmd("ps -o rss= -p " ++ os:getpid()))) * 1024.

-spec gates(map()) -> boolean().
gates(#{<<"gate_status">> := Status}) ->
    Status =/= <<"latency_failed">> andalso Status =/= <<"rss_growth_exceeded">>.

-spec gate_status([non_neg_integer()], non_neg_integer(), non_neg_integer(), non_neg_integer()) -> atom().
gate_status(Latencies, Errors, BaselineRss, Delta) ->
    P95 = percentile(Latencies, 95),
    P99 = percentile(Latencies, 99),
    if
        Errors > 0; P95 > 200000; P99 > 500000 -> latency_failed;
        Delta > 268435456 -> rss_growth_exceeded;
        BaselineRss > 268435456 -> baseline_rss_exceeded;
        true -> pass
    end.

-spec room(non_neg_integer()) -> binary().
room(N) -> <<"room-", (integer_to_binary(N))/binary>>.

-spec user(atom(), non_neg_integer()) -> binary().
user(Role, N) -> <<(atom_to_binary(Role))/binary, "-", (integer_to_binary(N))/binary>>.

-spec token(atom(), non_neg_integer()) -> binary().
token(Role, N) -> <<(user(Role, N))/binary, ":device", (atom_to_binary(Role))/binary>>.

-spec id(binary(), non_neg_integer()) -> binary().
id(Prefix, N) -> <<Prefix/binary, "-", (integer_to_binary(N))/binary>>.

-spec path(binary(), string()) -> binary().
path(Id, Action) -> <<"/api/v1/calls/", Id/binary, "/", (list_to_binary(Action))/binary>>.
