-module(vault_calls_metrics).
-moduledoc "Bounded Prometheus endpoint and Calls RPC/dependency measurements.".
-behaviour(gen_server).

-export([start_link/0, rpc/4, dependency/2, render/0, draining/0, drain/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-define(TABLE, vault_calls_metrics).
-define(BUCKETS_US, [1000, 5000, 10000, 25000, 50000, 100000, 200000, 500000, 1000000, 2000000, 5000000]).

-spec start_link() -> gen_server:start_ret().
start_link() -> gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

-spec rpc(ctx:t(), map(), map(), fun((ctx:t(), map()) -> term())) -> term().
rpc(Ctx, Request, Info, Fun) ->
    Method = maps:get(full_method, Info, <<"unknown">>),
    Started = erlang:monotonic_time(microsecond),
    try Fun(Ctx, Request) of
        Result ->
            observe_rpc(Method, result_code(Result), elapsed(Started)),
            Result
    catch
        Class:Reason:Stack ->
            observe_rpc(Method, <<"13">>, elapsed(Started)),
            erlang:raise(Class, Reason, Stack)
    end.

-spec dependency(auth | chat, fun(() -> term())) -> term().
dependency(Name, Fun) ->
    Started = erlang:monotonic_time(microsecond),
    try Fun() of
        Result ->
            observe_dependency(Name, dependency_code(Result), elapsed(Started)),
            Result
    catch
        Class:Reason:Stack ->
            observe_dependency(Name, internal, elapsed(Started)),
            erlang:raise(Class, Reason, Stack)
    end.

-spec render() -> binary().
render() ->
    ensure_table(),
    {Published, Dropped, Queued} = events(),
    {Active, ScrapeError} = active_calls(),
    Static = [
        "# HELP vault_calls_active Current unfinished calls.\n# TYPE vault_calls_active gauge\n",
        metric("vault_calls_active", [], Active),
        "# HELP vault_calls_capacity Maximum unfinished calls.\n# TYPE vault_calls_capacity gauge\n",
        metric("vault_calls_capacity", [], 200),
        "# HELP vault_calls_events_published_total Redis events published.\n# TYPE vault_calls_events_published_total counter\n",
        metric("vault_calls_events_published_total", [], Published),
        "# HELP vault_calls_events_dropped_total Redis events dropped by the bounded queue.\n# TYPE vault_calls_events_dropped_total counter\n",
        metric("vault_calls_events_dropped_total", [], Dropped),
        "# HELP vault_calls_event_queue Current queued Redis events.\n# TYPE vault_calls_event_queue gauge\n",
        metric("vault_calls_event_queue", [], Queued),
        "# HELP vault_calls_draining Whether new calls are rejected for shutdown.\n# TYPE vault_calls_draining gauge\n",
        metric("vault_calls_draining", [], bool(draining())),
        "# HELP vault_calls_scrape_errors_total Snapshot failures.\n# TYPE vault_calls_scrape_errors_total counter\n",
        metric("vault_calls_scrape_errors_total", [], ScrapeError),
        "# HELP vault_calls_grpc_requests_total Completed Calls RPCs.\n# TYPE vault_calls_grpc_requests_total counter\n",
        "# HELP vault_calls_grpc_request_duration_seconds Calls RPC duration.\n# TYPE vault_calls_grpc_request_duration_seconds histogram\n",
        "# HELP vault_calls_dependency_requests_total Calls dependency results.\n# TYPE vault_calls_dependency_requests_total counter\n",
        "# HELP vault_calls_dependency_duration_seconds Calls dependency duration.\n# TYPE vault_calls_dependency_duration_seconds histogram\n"
    ],
    Entries = lists:map(fun format_entry/1, lists:sort(ets:tab2list(?TABLE))),
    iolist_to_binary([Static, Entries]).

-spec draining() -> boolean().
draining() -> application:get_env(vault_calls, draining, false).

-spec drain() -> ok.
drain() ->
    ok = application:set_env(vault_calls, draining, true),
    drain_until(erlang:monotonic_time(millisecond) + 9000).

-spec init(list()) -> {ok, map()}.
init([]) ->
    ?TABLE = ets:new(?TABLE, [named_table, public, set, {write_concurrency, true}, {read_concurrency, true}]),
    case application:get_env(vault_calls, metrics_enabled, false) of
        true ->
            {Address, Port} = application:get_env(vault_calls, metrics_endpoint, {{127, 0, 0, 1}, 50056}),
            {ok, Listener} = gen_tcp:listen(Port, [binary, {packet, raw}, {active, false},
                {reuseaddr, true}, {ip, Address}, {backlog, 8}]),
            self() ! accept,
            {ok, #{listener => Listener}};
        false -> {ok, #{listener => undefined}}
    end.

-spec handle_call(term(), gen_server:from(), map()) -> {reply, term(), map()}.
handle_call(Request, _From, State) -> {reply, {error, {bad_call, Request}}, State}.

-spec handle_cast(term(), map()) -> {noreply, map()}.
handle_cast(_Request, State) -> {noreply, State}.

-spec handle_info(term(), map()) -> {noreply, map()}.
handle_info(accept, State = #{listener := Listener}) when is_port(Listener) ->
    case gen_tcp:accept(Listener, 1000) of
        {ok, Socket} ->
            ok = serve(Socket),
            self() ! accept,
            {noreply, State};
        {error, timeout} ->
            self() ! accept,
            {noreply, State};
        {error, closed} -> {noreply, State}
    end;
handle_info(_Message, State) -> {noreply, State}.

-spec terminate(term(), map()) -> ok.
terminate(_Reason, #{listener := Listener}) when is_port(Listener) ->
    _ = gen_tcp:close(Listener),
    ok;
terminate(_Reason, _State) -> ok.

-spec elapsed(integer()) -> non_neg_integer().
elapsed(Started) -> max(0, erlang:monotonic_time(microsecond) - Started).

-spec result_code(term()) -> binary().
result_code({ok, _, _}) -> <<"0">>;
result_code({grpc_error, {Code, _}}) when is_binary(Code) -> Code;
result_code(_) -> <<"13">>.

-spec dependency_code(term()) -> atom().
dependency_code(ok) -> ok;
dependency_code({ok, _}) -> ok;
dependency_code({error, Reason}) when is_atom(Reason) -> Reason;
dependency_code(_) -> internal.

-spec observe_rpc(binary(), binary(), non_neg_integer()) -> ok.
observe_rpc(Method, Code, Micros) ->
    increment({rpc_requests, Method, Code}, 1),
    observe_histogram(rpc, Method, Micros).

-spec observe_dependency(atom(), atom(), non_neg_integer()) -> ok.
observe_dependency(Name, Code, Micros) ->
    increment({dependency_requests, Name, Code}, 1),
    observe_histogram(dependency, Name, Micros).

-spec observe_histogram(rpc | dependency, atom() | binary(), non_neg_integer()) -> ok.
observe_histogram(Kind, Name, Micros) ->
    increment({Kind, count, Name}, 1),
    increment({Kind, sum, Name}, Micros),
    [increment({Kind, bucket, Name, Bucket}, 1) || Bucket <- ?BUCKETS_US, Micros =< Bucket],
    increment({Kind, bucket, Name, infinity}, 1),
    ok.

-spec increment(term(), integer()) -> ok.
increment(Key, Amount) ->
    ensure_table(),
    _ = ets:update_counter(?TABLE, Key, Amount, {Key, 0}),
    ok.

-spec ensure_table() -> ok.
ensure_table() ->
    case ets:whereis(?TABLE) of
        undefined ->
            try ets:new(?TABLE, [named_table, public, set, {write_concurrency, true}, {read_concurrency, true}]) of
                _ -> ok
            catch error:badarg -> ok
            end;
        _ -> ok
    end.

-spec format_entry({term(), integer()}) -> iolist().
format_entry({{rpc_requests, Method, Code}, Value}) ->
    metric("vault_calls_grpc_requests_total", [{"method", Method}, {"code", Code}], Value);
format_entry({{rpc, count, Method}, Value}) ->
    metric("vault_calls_grpc_request_duration_seconds_count", [{"method", Method}], Value);
format_entry({{rpc, sum, Method}, Value}) ->
    metric("vault_calls_grpc_request_duration_seconds_sum", [{"method", Method}], Value / 1000000);
format_entry({{rpc, bucket, Method, Bucket}, Value}) ->
    metric("vault_calls_grpc_request_duration_seconds_bucket",
        [{"method", Method}, {"le", bucket_seconds(Bucket)}], Value);
format_entry({{dependency_requests, Name, Code}, Value}) ->
    metric("vault_calls_dependency_requests_total", [{"dependency", Name}, {"code", Code}], Value);
format_entry({{dependency, count, Name}, Value}) ->
    metric("vault_calls_dependency_duration_seconds_count", [{"dependency", Name}], Value);
format_entry({{dependency, sum, Name}, Value}) ->
    metric("vault_calls_dependency_duration_seconds_sum", [{"dependency", Name}], Value / 1000000);
format_entry({{dependency, bucket, Name, Bucket}, Value}) ->
    metric("vault_calls_dependency_duration_seconds_bucket",
        [{"dependency", Name}, {"le", bucket_seconds(Bucket)}], Value);
format_entry(_) -> [].

-spec bucket_seconds(non_neg_integer() | infinity) -> binary().
bucket_seconds(infinity) -> <<"+Inf">>;
bucket_seconds(Micros) -> float_to_binary(Micros / 1000000, [short]).

-spec metric(string(), [{string(), term()}], number()) -> iolist().
metric(Name, Labels, Value) ->
    EncodedLabels = case Labels of
        [] -> [];
        _ -> ["{", lists:join(",", [[Key, "=", json:encode(label_value(Val))] || {Key, Val} <- Labels]), "}"]
    end,
    [Name, EncodedLabels, " ", number(Value), "\n"].

-spec label_value(term()) -> binary().
label_value(Value) when is_binary(Value) -> Value;
label_value(Value) when is_atom(Value) -> atom_to_binary(Value);
label_value(Value) ->
    case unicode:characters_to_binary(io_lib:format("~p", [Value])) of
        Binary when is_binary(Binary) -> Binary;
        _ -> <<"invalid">>
    end.

-spec number(number()) -> binary().
number(Value) when is_integer(Value) -> integer_to_binary(Value);
number(Value) -> float_to_binary(Value, [short]).

-spec bool(boolean()) -> 0 | 1.
bool(true) -> 1;
bool(false) -> 0.

-spec events() -> {non_neg_integer(), non_neg_integer(), non_neg_integer()}.
events() ->
    try vault_calls_events:counts() catch _:_ -> {0, 0, 0} end.

-spec active_calls() -> {non_neg_integer(), 0 | 1}.
active_calls() ->
    try vault_calls_store:list_active(vault_calls_store) of
        {ok, Ids} -> {length(Ids), 0};
        _ -> {0, 1}
    catch _:_ -> {0, 1}
    end.

-spec drain_until(integer()) -> ok.
drain_until(Deadline) ->
    case active_calls() of
        {0, 0} -> ok;
        _ ->
            case erlang:monotonic_time(millisecond) >= Deadline of
                true -> ok;
                false ->
                    timer:sleep(100),
                    drain_until(Deadline)
            end
    end.

-spec serve(gen_tcp:socket()) -> ok.
serve(Socket) ->
    Result = receive_request(Socket, <<>>),
    Response = case Result of
        {ok, <<"GET /metrics ", _/binary>>} -> {200, "OK", render()};
        {ok, _} -> {404, "Not Found", <<"not found\n">>};
        error -> {400, "Bad Request", <<"bad request\n">>}
    end,
    {Status, Text, Body} = Response,
    ok = gen_tcp:send(Socket, ["HTTP/1.1 ", integer_to_list(Status), " ", Text,
        "\r\ncontent-type: text/plain; version=0.0.4\r\ncontent-length: ",
        integer_to_list(byte_size(Body)), "\r\nconnection: close\r\n\r\n", Body]),
    gen_tcp:close(Socket).

-spec receive_request(gen_tcp:socket(), binary()) -> {ok, binary()} | error.
receive_request(_Socket, Data) when byte_size(Data) > 2048 -> error;
receive_request(Socket, Data) ->
    case binary:match(Data, <<"\r\n">>) of
        {Position, 2} -> {ok, binary:part(Data, 0, Position + 2)};
        nomatch ->
            case gen_tcp:recv(Socket, 0, 1000) of
                {ok, Part} -> receive_request(Socket, <<Data/binary, Part/binary>>);
                {error, _} -> error
            end
    end.
