-module(vault_calls_metrics_tests).

-include_lib("eunit/include/eunit.hrl").

metrics_test_() ->
    {setup, fun setup/0, fun cleanup/1, [
        fun rpc_and_dependency_metrics/0,
        fun failed_rpc_metrics/0,
        fun drain_gauge/0,
        fun http_metrics/0
    ]}.

setup() ->
    ok = application:set_env(vault_calls, metrics_enabled, true),
    ok = application:set_env(vault_calls, metrics_endpoint, {{127, 0, 0, 1}, 0}),
    {ok, Pid} = vault_calls_metrics:start_link(),
    Pid.

cleanup(Pid) ->
    ok = application:set_env(vault_calls, draining, false),
    ok = gen_server:stop(Pid).

rpc_and_dependency_metrics() ->
    Result = vault_calls_metrics:rpc(ctx:new(), #{}, #{full_method => <<"/calls.v1.CallsService/Get">>},
        fun(Ctx, _Request) -> {ok, #{}, Ctx} end),
    ?assertMatch({ok, #{}, _}, Result),
    ok = vault_calls_metrics:dependency(auth, fun() -> ok end),
    Output = vault_calls_metrics:render(),
    ?assertNotEqual(nomatch, binary:match(Output,
        <<"vault_calls_grpc_requests_total{method=\"/calls.v1.CallsService/Get\",code=\"0\"} 1">>)),
    ?assertNotEqual(nomatch, binary:match(Output,
        <<"vault_calls_dependency_requests_total{dependency=\"auth\",code=\"ok\"} 1">>)),
    ?assertNotEqual(nomatch, binary:match(Output,
        <<"vault_calls_grpc_request_duration_seconds_bucket{method=\"/calls.v1.CallsService/Get\",le=\"+Inf\"} 1">>)).

failed_rpc_metrics() ->
    Result = vault_calls_metrics:rpc(ctx:new(), #{}, #{full_method => <<"/calls.v1.CallsService/Create">>},
        fun(_Ctx, _Request) -> {grpc_error, {<<"14">>, <<"unavailable">>}} end),
    ?assertEqual({grpc_error, {<<"14">>, <<"unavailable">>}}, Result),
    Output = vault_calls_metrics:render(),
    ?assertNotEqual(nomatch, binary:match(Output,
        <<"vault_calls_grpc_requests_total{method=\"/calls.v1.CallsService/Create\",code=\"14\"} 1">>)).

drain_gauge() ->
    ?assertEqual(false, vault_calls_metrics:draining()),
    ok = application:set_env(vault_calls, draining, true),
    ?assertEqual(true, vault_calls_metrics:draining()),
    ?assertNotEqual(nomatch, binary:match(vault_calls_metrics:render(), <<"vault_calls_draining 1">>)).

http_metrics() ->
    #{listener := Listener} = sys:get_state(vault_calls_metrics),
    {ok, Port} = inet:port(Listener),
    {ok, Socket} = gen_tcp:connect({127, 0, 0, 1}, Port, [binary, {active, false}], 1000),
    ok = gen_tcp:send(Socket, "GET /metrics HTTP/1.1\r\nhost: localhost\r\n\r\n"),
    {ok, Response} = gen_tcp:recv(Socket, 0, 2000),
    ?assertNotEqual(nomatch, binary:match(Response, <<"HTTP/1.1 200 OK">>)),
    ?assertNotEqual(nomatch, binary:match(Response, <<"vault_calls_capacity 200">>)),
    gen_tcp:close(Socket).
