-module(vault_calls_http).
-moduledoc "Loopback HTTP prototype for the Calls control plane.".
-behaviour(gen_server).

-export([start_link/0, port/0, route/2, status_line/1]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-type state() :: #{listener := gen_tcp:socket(), port := non_neg_integer(),
                   connections := #{reference() => gen_tcp:socket()}}.

-spec start_link() -> gen_server:start_ret().
start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

-spec port() -> non_neg_integer().
port() ->
    gen_server:call(?MODULE, port, 5000).

-spec init([]) -> {ok, state()} | ignore | {error, term()}.
init([]) ->
    case application:get_env(vault_calls, http_enabled, false) of
        false -> ignore;
        true -> start_server()
    end.

-spec handle_call(term(), gen_server:from(), state()) -> {reply, term(), state()}.
handle_call(port, _From, State) ->
    {reply, maps:get(port, State), State};
handle_call(_Request, _From, State) ->
    {reply, {error, badarg}, State}.

-spec handle_cast(term(), state()) -> {noreply, state()}.
handle_cast(_Request, State) ->
    {noreply, State}.

-spec handle_info(term(), state()) -> {noreply, state()} | {stop, normal, state()}.
handle_info(accept, #{connections := Connections} = State) when map_size(Connections) >= 200 ->
    {noreply, State};
handle_info(accept, #{listener := Listener, connections := Connections} = State) ->
    case gen_tcp:accept(Listener, 1000) of
        {ok, Socket} ->
            {Pid, Ref} = spawn_monitor(fun() ->
                receive go -> vault_calls_http_connection:serve(Socket) end
            end),
            ok = gen_tcp:controlling_process(Socket, Pid),
            Pid ! go,
            self() ! accept,
            {noreply, State#{connections := maps:put(Ref, Socket, Connections)}};
        {error, timeout} ->
            self() ! accept,
            {noreply, State};
        {error, closed} -> {stop, normal, State}
    end;
handle_info({'DOWN', Ref, process, _Pid, _Reason}, #{connections := Connections} = State) ->
    {noreply, State#{connections := maps:remove(Ref, Connections)}};
handle_info(_Message, State) ->
    {noreply, State}.

-spec terminate(term(), state()) -> ok.
terminate(_Reason, #{listener := Listener, connections := Connections}) ->
    _ = gen_tcp:close(Listener),
    _ = [gen_tcp:close(Socket) || Socket <- maps:values(Connections)],
    ok.

-spec start_server() -> {ok, state()} | {error, term()}.
start_server() ->
    Options = [binary, {packet, raw}, {active, false}, {reuseaddr, true},
               {ip, {127, 0, 0, 1}}, {backlog, 200}],
    case gen_tcp:listen(0, Options) of
        {ok, Listener} ->
            {ok, Port} = inet:port(Listener),
            self() ! accept,
            {ok, #{listener => Listener, port => Port, connections => #{}}};
        {error, _} = Error -> Error
    end.

-spec route(list(), term()) -> {non_neg_integer(), map(), iodata()}.
route(Env, Input) ->
    Method = to_binary(proplists:get_value(request_method, Env, <<>>)),
    Uri = to_binary(proplists:get_value(script_name, Env, <<>>)),
    case authenticate(Env) of
        {ok, Identity} -> route_method(Method, Uri, Identity, Input);
        {error, Reason} -> error_result(Reason)
    end.

-spec to_binary(binary() | string()) -> binary().
to_binary(Value) when is_binary(Value) -> Value;
to_binary(Value) when is_list(Value) -> list_to_binary(Value).

-spec authenticate(list()) -> {ok, {binary(), binary()}} | {error, unauthorized}.
authenticate(Env) ->
    case proplists:get_value(http_authorization, Env, undefined) of
        "Bearer prototype:" ++ Token -> vault_calls_auth_stub:validate(list_to_binary("prototype:" ++ Token));
        _ -> {error, unauthorized}
    end.

-spec route_method(binary(), binary(), {binary(), binary()}, term()) ->
    {non_neg_integer(), map(), iodata()}.
route_method(<<"POST">>, Uri, Identity, Input) ->
    case decode_body(Input) of
        {ok, Body} ->
            {Path, _} = split_uri(Uri),
            post_route(Path, Uri, Identity, Body);
        {error, Reason} -> error_result(Reason)
    end;
route_method(<<"GET">>, Uri, Identity, _Input) ->
    {Path, Query} = split_uri(Uri),
    get_route(Path, Query, Identity);
route_method(_Method, _Uri, _Identity, _Input) ->
    {405, #{<<"error">> => <<"method_not_allowed">>}, []}.

-spec post_route(binary(), binary(), {binary(), binary()}, map()) ->
    {non_neg_integer(), map(), iodata()}.
post_route(<<"/api/v1/calls">>, Uri, {User, Device}, Body) ->
    create(Uri, User, Device, Body);
post_route(Path, Uri, {User, Device}, Body) ->
    case binary:split(Path, <<"/">>, [global]) of
        [<<>>, <<"api">>, <<"v1">>, <<"calls">>, Id, Action] -> command(Uri, Id, Action, User, Device, Body);
        _ -> error_result(not_found)
    end.

-spec create(binary(), binary(), binary(), map()) -> {non_neg_integer(), map(), iodata()}.
create(Uri, User, Device, Body) ->
    case {binary_field(Body, <<"room_id">>, 128), binary_field(Body, <<"command_id">>, 128)} of
        {{ok, RoomId}, {ok, CommandId}} ->
            case vault_calls_chat_stub:get_room(RoomId, User) of
                {ok, Callee} ->
                    Fingerprint = fingerprint(Uri, Body),
                    Id = binary:encode_hex(crypto:hash(sha256, [User, RoomId, CommandId, Fingerprint])),
                    Args = #{id => Id, room_id => RoomId, caller => User, caller_device => Device,
                             callee => Callee, command_id => CommandId, fingerprint => Fingerprint},
                    case vault_call_sup:start_call(Args) of
                        {ok, _} -> {201, #{<<"call_id">> => Id, <<"state">> => <<"ringing">>}, []};
                        {error, Reason} -> error_result(Reason)
                    end;
                {error, Reason} -> error_result(Reason)
            end;
        _ -> error_result(badarg)
    end.

-spec command(binary(), binary(), binary(), binary(), binary(), map()) ->
    {non_neg_integer(), map(), iodata()}.
command(_Uri, Id, <<"signal">>, User, Device, Body) ->
    case {kind(Body), maps:get(<<"payload">>, Body, undefined), maps:get(<<"sender_seq">>, Body, undefined)} of
        {{ok, Kind}, Payload, SenderSeq} when is_binary(Payload), is_integer(SenderSeq), SenderSeq > 0 ->
            reply_result(vault_call:signal(Id, User, Device, Kind, Payload, SenderSeq));
        _ -> error_result(badarg)
    end;
command(Uri, Id, Action, User, Device, Body) ->
    case {command_name(Action), binary_field(Body, <<"command_id">>, 128)} of
        {{ok, accept}, {ok, _}} -> with_chat(Id, User, fun() -> run_command(Uri, Id, accept, User, Device, Body) end);
        {{ok, Name}, {ok, _}} -> run_command(Uri, Id, Name, User, Device, Body);
        {{ok, _}, _} -> error_result(badarg);
        _ -> error_result(not_found)
    end.

-spec run_command(binary(), binary(), atom(), binary(), binary(), map()) ->
    {non_neg_integer(), map(), iodata()}.
run_command(Uri, Id, Name, User, Device, Body) ->
    {ok, CommandId} = binary_field(Body, <<"command_id">>, 128),
    Meta = #{device => Device, command_id => CommandId, fingerprint => fingerprint(Uri, Body)},
    reply_result(vault_call:command(Id, {Name, User, Device}, Meta)).

-spec with_chat(binary(), binary(), fun(() -> {non_neg_integer(), map(), iodata()})) ->
    {non_neg_integer(), map(), iodata()}.
with_chat(Id, User, Fun) ->
    case authorized_call(Id, User) of
        {ok, Data} ->
            case vault_calls_chat_stub:get_room(maps:get(room_id, Data), User) of
                {ok, _} -> Fun();
                {error, Reason} -> error_result(Reason)
            end;
        {error, Reason} -> error_result(Reason)
    end.

-spec get_route(binary(), binary(), {binary(), binary()}) -> {non_neg_integer(), map(), iodata()}.
get_route(Path, Query, {User, _Device}) ->
    case binary:split(Path, <<"/">>, [global]) of
        [<<>>, <<"api">>, <<"v1">>, <<"calls">>, Id] ->
            case authorized_call(Id, User) of
                {ok, _} -> reply_result(vault_call:state(Id));
                {error, Reason} -> error_result(Reason)
            end;
        [<<>>, <<"api">>, <<"v1">>, <<"calls">>, Id, <<"signals">>] ->
            case {authorized_call(Id, User), parse_after(Query)} of
                {{ok, _}, {ok, After}} ->
                    case vault_calls_store:signals_after(vault_calls_store, Id, After) of
                        {ok, Signals} -> {200, #{<<"signals">> => [public_signal(S) || S <- Signals]}, []};
                        {error, Reason} -> error_result(Reason)
                    end;
                {{error, Reason}, _} -> error_result(Reason);
                {_, {error, Reason}} -> error_result(Reason)
            end;
        _ -> error_result(not_found)
    end.

-spec authorized_call(binary(), binary()) -> {ok, map()} | {error, not_found | unavailable}.
authorized_call(Id, User) ->
    case vault_calls_store:get_call(vault_calls_store, Id) of
        {ok, #{caller := User} = Data} -> {ok, Data};
        {ok, #{callee := User} = Data} -> {ok, Data};
        {ok, _} -> {error, not_found};
        {error, Reason} -> {error, Reason}
    end.

-spec parse_after(binary()) -> {ok, non_neg_integer()} | {error, badarg}.
parse_after(<<>>) -> {ok, 0};
parse_after(<<"after=", Value/binary>>) ->
    try binary_to_integer(Value) of
        After when After >= 0 -> {ok, After};
        _ -> {error, badarg}
    catch
        _:_ -> {error, badarg}
    end;
parse_after(_Query) -> {error, badarg}.

-spec decode_body(term()) -> {ok, map()} | {error, badarg}.
decode_body(Input) ->
    try json:decode(iolist_to_binary(Input)) of
        Body when is_map(Body) -> {ok, Body};
        _ -> {error, badarg}
    catch
        _:_ -> {error, badarg}
    end.

-spec binary_field(map(), binary(), pos_integer()) -> {ok, binary()} | error.
binary_field(Body, Key, Limit) ->
    case maps:get(Key, Body, undefined) of
        Value when is_binary(Value), byte_size(Value) > 0, byte_size(Value) =< Limit -> {ok, Value};
        _ -> error
    end.

-spec kind(map()) -> {ok, atom()} | error.
kind(Body) ->
    case maps:get(<<"kind">>, Body, undefined) of
        <<"offer">> -> {ok, offer};
        <<"answer">> -> {ok, answer};
        <<"candidate">> -> {ok, candidate};
        <<"end-of-candidates">> -> {ok, end_of_candidates};
        _ -> error
    end.

-spec command_name(binary()) -> {ok, atom()} | error.
command_name(<<"accept">>) -> {ok, accept};
command_name(<<"reject">>) -> {ok, reject};
command_name(<<"cancel">>) -> {ok, cancel};
command_name(<<"connected">>) -> {ok, connected};
command_name(<<"end">>) -> {ok, 'end'};
command_name(<<"heartbeat">>) -> {ok, heartbeat};
command_name(_) -> error.

-spec fingerprint(binary(), map()) -> binary().
fingerprint(Uri, Body) ->
    Canonical = iolist_to_binary(json:encode(Body)),
    crypto:hash(sha256, [<<"POST ">>, Uri, <<"\n">>, Canonical]).

-spec reply_result({ok, atom()} | {ok, atom(), pos_integer()} | {error, atom()}) ->
    {non_neg_integer(), map(), iodata()}.
reply_result({ok, State}) -> {200, #{<<"state">> => atom_to_binary(State)}, []};
reply_result({ok, State, SignalSeq}) ->
    {200, #{<<"state">> => atom_to_binary(State), <<"signal_seq">> => SignalSeq}, []};
reply_result({error, Reason}) -> error_result(Reason).

-spec error_result(atom()) -> {non_neg_integer(), map(), iodata()}.
error_result(unauthorized) -> {401, #{<<"error">> => <<"unauthorized">>}, []};
error_result(not_found) -> {404, #{<<"error">> => <<"not_found">>}, []};
error_result(busy) -> {409, #{<<"error">> => <<"busy">>}, []};
error_result(conflict) -> {409, #{<<"error">> => <<"conflict">>}, []};
error_result(seq_reused) -> {409, #{<<"error">> => <<"seq_reused">>}, []};
error_result(signal_limit) -> {409, #{<<"error">> => <<"signal_limit">>}, []};
error_result(candidate_limit) -> {409, #{<<"error">> => <<"candidate_limit">>}, []};
error_result(signal_rate_limited) -> {429, #{<<"error">> => <<"signal_rate_limited">>}, []};
error_result(command_id_reused) -> {422, #{<<"error">> => <<"command_id_reused">>}, []};
error_result(capacity) -> {503, #{<<"error">> => <<"capacity">>}, "retry-after: 5\r\n"};
error_result(unavailable) -> {503, #{<<"error">> => <<"unavailable">>}, []};
error_result(badarg) -> {400, #{<<"error">> => <<"badarg">>}, []};
error_result(_) -> {500, #{<<"error">> => <<"internal">>}, []}.

-spec public_signal(map()) -> map().
public_signal(Signal) ->
    #{<<"signal_seq">> => maps:get(signal_seq, Signal),
      <<"sender_device">> => maps:get(sender_device, Signal),
      <<"sender_seq">> => maps:get(sender_seq, Signal),
      <<"kind">> => atom_to_binary(maps:get(kind, Signal)),
      <<"payload">> => maps:get(payload, Signal)}.

-spec split_uri(binary()) -> {binary(), binary()}.
split_uri(Uri) ->
    case binary:split(Uri, <<"?">>) of
        [Path, Query] -> {Path, Query};
        [Path] -> {Path, <<>>}
    end.

-spec status_line(non_neg_integer()) -> string().
status_line(200) -> "status: 200 OK";
status_line(201) -> "status: 201 Created";
status_line(400) -> "status: 400 Bad Request";
status_line(401) -> "status: 401 Unauthorized";
status_line(404) -> "status: 404 Not Found";
status_line(405) -> "status: 405 Method Not Allowed";
status_line(409) -> "status: 409 Conflict";
status_line(413) -> "status: 413 Payload Too Large";
status_line(422) -> "status: 422 Unprocessable Entity";
status_line(429) -> "status: 429 Too Many Requests";
status_line(500) -> "status: 500 Internal Server Error";
status_line(503) -> "status: 503 Service Unavailable".
