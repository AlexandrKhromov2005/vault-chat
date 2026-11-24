-module(vault_calls_config).
-moduledoc "Validated environment and supervision children for production Calls.".

-export([load/0, channel_specs/0, event_specs/0, monitor_specs/0, grpc_specs/0, endpoint/1, port/1]).

-spec load() -> ok.
load() ->
    ok = copy_env("CALLS_DATABASE_URL", database_url, fun unicode:characters_to_binary/1),
    ok = copy_env("CALLS_RUNTIME_DB_PASSWORD", runtime_password, fun unicode:characters_to_binary/1),
    ok = copy_env("CALLS_AUTH_GRPC_ADDR", auth_endpoint, fun endpoint/1),
    ok = copy_env("CALLS_CHAT_GRPC_ADDR", chat_endpoint, fun endpoint/1),
    ok = copy_env("CALLS_GRPC_PORT", grpc_port, fun port/1),
    ok = copy_env("CALLS_GRPC_ENABLED", grpc_enabled, fun boolean/1),
    ok = copy_env("CALLS_HTTP_ENABLED", http_enabled, fun boolean/1),
    ok = copy_env("CALLS_PRODUCTION", production, fun boolean/1),
    ok = copy_env("CALLS_REDIS_ADDR", redis_endpoint, fun endpoint/1),
    ok = copy_env("CALLS_REDIS_PASSWORD", redis_password),
    ok = copy_env("CALLS_MONITOR_ENABLED", monitor_enabled, fun boolean/1),
    ok = copy_env("CALLS_METRICS_ENABLED", metrics_enabled, fun boolean/1),
    ok = copy_env("CALLS_METRICS_ADDR", metrics_endpoint, fun metrics_address/1),
    ok = copy_env("CALLS_TURN_URIS", turn_uris, fun uri_list/1),
    ok = turn_secret_env(),
    ok = tls_env(),
    case application:get_env(vault_calls, production, false) of
        true ->
            ok = application:set_env(vault_calls, auth_mod, vault_calls_auth),
            ok = application:set_env(vault_calls, chat_mod, vault_calls_chat);
        false -> ok
    end,
    case application:get_env(vault_calls, metrics_enabled, undefined) of
        undefined -> application:set_env(vault_calls, metrics_enabled,
            application:get_env(vault_calls, production, false));
        _ -> ok
    end.

-spec channel_specs() -> [supervisor:child_spec()].
channel_specs() ->
    case application:get_env(vault_calls, production, false) of
        false -> [];
        true -> [channel_spec(calls_auth, auth_endpoint), channel_spec(calls_chat, chat_endpoint)]
    end.

-spec event_specs() -> [supervisor:child_spec()].
event_specs() ->
    case application:get_env(vault_calls, redis_endpoint, undefined) of
        undefined -> [];
        _ -> [#{id => vault_calls_events, start => {vault_calls_events, start_link, []}}]
    end.

-spec monitor_specs() -> [supervisor:child_spec()].
monitor_specs() ->
    Enabled = application:get_env(vault_calls, monitor_enabled, false) orelse
              application:get_env(vault_calls, production, false),
    case Enabled of
        true -> [#{id => vault_calls_monitor, start => {vault_calls_monitor, start_link, []}}];
        false -> []
    end.

-spec grpc_specs() -> [supervisor:child_spec()].
grpc_specs() ->
    case application:get_env(vault_calls, grpc_enabled, false) of
        false -> [];
        true -> [grpc_spec(), health_spec()]
    end.

-spec channel_spec(atom(), atom()) -> supervisor:child_spec().
channel_spec(Name, Key) ->
    {Host, Port} = application:get_env(vault_calls, Key, undefined),
    Endpoint = case application:get_env(vault_calls, tls_mode, plaintext) of
        plaintext -> {http, Host, Port, []};
        mtls -> {https, Host, Port, vault_calls_tls:client_options(target(Key), tls_dir())}
    end,
    #{id => Name, start => {vault_calls_channel, start_link, [Name, [Endpoint], #{}]}}.

-spec grpc_spec() -> supervisor:child_spec().
grpc_spec() ->
    Grpc = #{service_protos => [calls_pb, grpcbox_health_pb],
             services => #{'calls.v1.CallsService' => vault_calls_rpc,
                           'grpc.health.v1.Health' => vault_calls_health},
             unary_interceptor => fun vault_calls_metrics:rpc/4},
    Transport = case application:get_env(vault_calls, tls_mode, plaintext) of
        plaintext -> #{ssl => false};
        mtls -> (maps:from_list(vault_calls_tls:server_options(tls_dir())))#{ssl => true}
    end,
    GrpcWithAuth = case maps:get(ssl, Transport) of
        true -> Grpc#{auth_fun => fun vault_calls_tls:gateway_auth/1};
        false -> Grpc
    end,
    Listen = #{port => application:get_env(vault_calls, grpc_port, 50054), ip => {0, 0, 0, 0}},
    Pool = #{size => 8},
    Server = #{max_concurrent_streams => 200, max_header_list_size => 8192,
               max_frame_size => 16384, initial_window_size => 65535},
    vault_calls_grpc:child_spec(Server, GrpcWithAuth, Listen, Pool, Transport).

-spec health_spec() -> supervisor:child_spec().
health_spec() ->
    Spec = grpcbox:server_child_spec(#{max_concurrent_streams => 10},
        #{service_protos => [grpcbox_health_pb],
          services => #{'grpc.health.v1.Health' => vault_calls_health}},
        #{port => 50055, ip => {127, 0, 0, 1}}, #{size => 1}, #{ssl => false}),
    Spec#{id => calls_health}.

-spec endpoint(string()) -> {string(), 1..65535}.
endpoint(Value) ->
    WithoutScheme = lists:foldl(fun(Prefix, Acc) ->
        case lists:prefix(Prefix, Acc) of
            true -> lists:nthtail(length(Prefix), Acc);
            false -> Acc
        end
    end, Value, ["passthrough:///", "dns:///"]),
    case string:split(WithoutScheme, ":", trailing) of
        [Host, Port] when Host =/= [], length(Host) =< 255 -> {Host, port(Port)};
        _ -> error(invalid_grpc_endpoint)
    end.

-spec port(string()) -> 1..65535.
port(Value) ->
    case string:to_integer(Value) of
        {Port, []} when is_integer(Port), Port > 0, Port =< 65535 -> Port;
        _ -> error(invalid_port)
    end.

-spec metrics_address(string()) -> {inet:ip_address(), 1..65535}.
metrics_address(Value) ->
    {Host, Port} = endpoint(Value),
    case inet:parse_address(Host) of
        {ok, Address} -> {Address, Port};
        {error, _} -> error(invalid_metrics_address)
    end.

-spec boolean(string()) -> boolean().
boolean("true") -> true;
boolean("false") -> false;
boolean(_) -> error(invalid_boolean).

-spec copy_env(string(), atom()) -> ok.
copy_env(OsName, Key) -> copy_env(OsName, Key, fun(Value) -> Value end).

-spec copy_env(string(), atom(), fun((string()) -> term())) -> ok.
copy_env(OsName, Key, Convert) ->
    case os:getenv(OsName) of
        false -> ok;
        Value -> application:set_env(vault_calls, Key, Convert(Value))
    end.

-spec uri_list(string()) -> [binary()].
uri_list(Value) ->
    Parts = [string:trim(Part) || Part <- string:split(Value, ",", all)],
    case length(Parts) =< 8 of
        true -> lists:map(fun uri/1, Parts);
        false -> error(too_many_turn_uris)
    end.

-spec uri(string()) -> binary().
uri(Value) when length(Value) > 0, length(Value) =< 256 ->
    case re:run(Value, "^(turn|turns|stun):([A-Za-z0-9.-]+|\\[[0-9A-Fa-f:]+\\])(:[0-9]{1,5})?(\\?[A-Za-z0-9=&_-]+)?$") of
        {match, _} ->
            case unicode:characters_to_binary(Value) of
                Binary when is_binary(Binary) -> Binary;
                _ -> error(invalid_turn_uri)
            end;
        nomatch -> error(invalid_turn_uri)
    end;
uri(_) -> error(invalid_turn_uri).

-spec turn_secret_env() -> ok.
turn_secret_env() ->
    case os:getenv("CALLS_TURN_SECRET_FILE") of
        false -> ok;
        Path ->
            {ok, File} = file:open(Path, [read, binary, raw]),
            Result = file:read(File, 4097),
            ok = file:close(File),
            case Result of
                {ok, Data} when byte_size(Data) > 0, byte_size(Data) =< 4096 ->
                    application:set_env(vault_calls, turn_secret, string:trim(Data));
                _ -> error(invalid_turn_secret)
            end
    end.

-spec tls_env() -> ok.
tls_env() ->
    case os:getenv("RPC_TLS_MODE") of
        false -> ok;
        "plaintext" -> application:set_env(vault_calls, tls_mode, plaintext);
        "mtls" ->
            case os:getenv("RPC_TLS_DIR") of
                false -> error(missing_rpc_tls_dir);
                "" -> error(missing_rpc_tls_dir);
                Dir -> application:set_env(vault_calls, tls_mode, mtls),
                       application:set_env(vault_calls, tls_dir, Dir)
            end;
        _ -> error(invalid_rpc_tls_mode)
    end.

-spec tls_dir() -> string().
tls_dir() -> application:get_env(vault_calls, tls_dir, undefined).

-spec target(atom()) -> auth | chat.
target(auth_endpoint) -> auth;
target(chat_endpoint) -> chat.
