-module(vault_calls_grpc).
-moduledoc "Calls service supervisor with a configurable TLS acceptor pool.".
-behaviour(supervisor).

-include_lib("grpcbox/include/grpcbox.hrl").

-export([child_spec/5, start_link/5]).
-export([init/1]).

-spec child_spec(term(), map(), map(), map(), map()) -> supervisor:child_spec().
child_spec(ServerOpts, GrpcOpts, ListenOpts, PoolOpts, TransportOpts) ->
    #{id => vault_calls_grpc, start => {?MODULE, start_link, [ServerOpts, GrpcOpts, ListenOpts, PoolOpts, TransportOpts]},
      type => supervisor, restart => permanent, shutdown => 1000}.

-spec start_link(term(), map(), map(), map(), map()) -> supervisor:startlink_ret().
start_link(ServerOpts, GrpcOpts, ListenOpts, PoolOpts, TransportOpts) ->
    supervisor:start_link(?MODULE, [ServerOpts, GrpcOpts, ListenOpts, PoolOpts, TransportOpts]).

-spec init(list()) -> {ok, {supervisor:sup_flags(), [supervisor:child_spec()]}}.
init([ServerOpts, GrpcOpts, ListenOpts, PoolOpts, TransportOpts]) ->
    Table = ets:new(vault_calls_services, [protected, named_table, set, {keypos, 2}, {read_concurrency, true}]),
    ok = load_services(maps:get(service_protos, GrpcOpts), maps:get(services, GrpcOpts), Table),
    AuthFun = case maps:get(ssl, TransportOpts, false) of
        true -> maps:get(auth_fun, GrpcOpts);
        false -> undefined
    end,
    Chatterbox = #{stream_callback_mod => grpcbox_stream,
                   stream_callback_opts => [Table, AuthFun,
                       maps:get(unary_interceptor, GrpcOpts, undefined), undefined, undefined]},
    Pool = vault_calls_pool,
    Children = [
        #{id => vault_calls_pool, start => {vault_calls_pool, start_link, [Pool, ServerOpts, Chatterbox, TransportOpts]}},
        #{id => vault_calls_socket, start => {vault_calls_socket, start_link, [Pool, ListenOpts, PoolOpts]}}
    ],
    {ok, {#{strategy => rest_for_one}, Children}}.

-spec load_services([module()], map(), ets:tid()) -> ok.
load_services([], _Services, _Table) -> ok;
load_services([Proto | Rest], Services, Table) ->
    lists:foreach(fun(ServiceName) -> load_service(Proto, ServiceName, Services, Table) end,
                  Proto:get_service_names()),
    load_services(Rest, Services, Table).

-spec load_service(module(), atom(), map(), ets:tid()) -> ok.
load_service(Proto, ServiceName, Services, Table) ->
    case maps:find(ServiceName, Services) of
        error -> ok;
        {ok, Module} ->
            {{service, _}, Methods} = Proto:get_service_def(ServiceName),
            lists:foreach(fun(Method) -> load_method(Proto, ServiceName, Module, Method, Table) end, Methods),
            ok
    end.

-spec load_method(module(), atom(), module(), map(), ets:tid()) -> ok.
load_method(Proto, ServiceName, Module, Method, Table) ->
    Name = maps:get(name, Method),
    Function = snake_case(atom_to_list(Name)),
    true = lists:member({Function, 2}, Module:module_info(exports)),
    Entry = #method{key = {atom_to_binary(ServiceName, utf8), atom_to_binary(Name, utf8)},
                    module = Module, function = Function, proto = Proto,
                    input = {maps:get(input, Method), maps:get(input_stream, Method)},
                    output = {maps:get(output, Method), maps:get(output_stream, Method)},
                    opts = maps:get(opts, Method)},
    true = ets:insert(Table, Entry),
    ok.

-spec snake_case(string()) -> atom().
snake_case(Name) ->
    Snaked = lists:foldl(fun(RE, Acc) -> re:replace(Acc, RE, "\\1_\\2", [{return, list}, global]) end,
                         Name, ["(.)([A-Z][a-z]+)", "(.)([0-9]+)", "([a-z0-9])([A-Z])"]),
    list_to_atom(string:lowercase(Snaked)).
