-module(vault_calls_events).
-moduledoc "Bounded best-effort publication of committed Calls changes to Redis Pub/Sub.".
-behaviour(gen_server).

-export([start_link/0, start_link/1, call_changed/1, signal/2, counts/0, stop/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-define(MAX_QUEUE, 1024).

-type event() :: map().
-type state() :: map().

-spec start_link() -> gen_server:start_ret().
start_link() -> start_link(#{}).

-spec start_link(map()) -> gen_server:start_ret().
start_link(Options) ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, Options, []).

-spec call_changed(map()) -> ok.
call_changed(Data) ->
    Type = case maps:get(state, Data) of
        ringing -> <<"call.created">>;
        connecting -> <<"call.accepted">>;
        active -> <<"call.connected">>;
        ended -> <<"call.ended">>
    end,
    Event = #{type => Type, call_id => maps:get(id, Data), room_id => maps:get(room_id, Data),
              state => atom_to_binary(maps:get(state, Data)), state_version => maps:get(state_version, Data)},
    publish([maps:get(caller, Data), maps:get(callee, Data)], Event),
    case maps:get(state, Data) of
        ringing ->
            Payload = iolist_to_binary(json:encode(#{room_id => maps:get(room_id, Data),
                user_ids => [maps:get(callee, Data)]})),
            publish_to(<<"push:signal:v1">>, Payload);
        _ -> ok
    end.

-spec signal(map(), map()) -> ok.
signal(Data, Signal) ->
    Event = #{type => <<"call.signal">>, call_id => maps:get(id, Data), room_id => maps:get(room_id, Data),
              signal => #{signal_seq => maps:get(signal_seq, Signal), sender_seq => maps:get(sender_seq, Signal),
                          kind => kind_binary(maps:get(kind, Signal)), payload => maps:get(payload, Signal)}},
    publish([maps:get(caller, Data), maps:get(callee, Data)], Event).

-spec counts() -> {non_neg_integer(), non_neg_integer(), non_neg_integer()}.
counts() ->
    case whereis(?MODULE) of
        undefined -> {0, 0, 0};
        _ -> gen_server:call(?MODULE, counts, 1000)
    end.

-spec stop() -> ok.
stop() ->
    case whereis(?MODULE) of
        undefined -> ok;
        _ -> gen_server:stop(?MODULE)
    end.

-spec init(map()) -> {ok, state()}.
init(#{publisher := Publisher}) ->
    {ok, #{client => undefined, publisher => Publisher, queue => queue:new(),
           publishing => false, published => 0, dropped => 0}};
init(_) ->
    process_flag(trap_exit, true),
    {Host, Port} = application:get_env(vault_calls, redis_endpoint, undefined),
    Password = application:get_env(vault_calls, redis_password, ""),
    Options = [{host, Host}, {port, Port}, {password, Password},
               {connect_timeout, 1000}, {reconnect_sleep, 250}],
    {Client, Publisher} = connect(Options),
    {ok, #{client => Client, publisher => Publisher, options => Options, queue => queue:new(),
           publishing => false, published => 0, dropped => 0}}.

-spec handle_call(term(), gen_server:from(), state()) -> {reply, term(), state()}.
handle_call(counts, _From, State) ->
    {reply, {maps:get(published, State), maps:get(dropped, State), queue:len(maps:get(queue, State))}, State};
handle_call(_Request, _From, State) -> {reply, {error, badarg}, State}.

-spec handle_cast(term(), state()) -> {noreply, state()}.
handle_cast({publish, Channel, Payload}, State) ->
    Queue = maps:get(queue, State),
    case queue:len(Queue) < ?MAX_QUEUE of
        false -> {noreply, State#{dropped := maps:get(dropped, State) + 1}};
        true ->
            Next = State#{queue := queue:in({Channel, Payload}, Queue)},
            self() ! publish,
            {noreply, Next}
    end;
handle_cast(_Request, State) -> {noreply, State}.

-spec handle_info(term(), state()) -> {noreply, state()}.
handle_info(publish, State) ->
    case queue:out(maps:get(queue, State)) of
        {empty, _} -> {noreply, State#{publishing := false}};
        {{value, {Channel, Payload}}, Rest} ->
            Publisher = maps:get(publisher, State),
            _ = Publisher(Channel, Payload),
            self() ! publish,
            {noreply, State#{queue := Rest, publishing := true,
                             published := maps:get(published, State) + 1}}
    end;
handle_info(reconnect, State) ->
    {Client, Publisher} = connect(maps:get(options, State)),
    {noreply, State#{client := Client, publisher := Publisher}};
handle_info({'EXIT', Client, _Reason}, State) ->
    case maps:get(client, State) of
        Client ->
            _ = timer:send_after(250, reconnect),
            {noreply, State#{client := undefined, publisher := fun(_, _) ->
                logger:warning("Calls event skipped while Redis is unavailable"),
                ok
            end}};
        _ -> {noreply, State}
    end;
handle_info(_Message, State) -> {noreply, State}.

-spec terminate(term(), state()) -> ok.
terminate(_Reason, State) ->
    case maps:get(client, State) of
        undefined -> ok;
        Client -> _ = gen_tcp:close(Client), ok
    end.

-spec connect(list()) -> {gen_tcp:socket() | undefined, fun((binary(), binary()) -> ok)}.
connect(Options) ->
    Host = proplists:get_value(host, Options),
    Port = proplists:get_value(port, Options),
    Password = proplists:get_value(password, Options),
    case gen_tcp:connect(Host, Port, [binary, {packet, raw}, {active, false}], 1000) of
        {ok, Socket} ->
            case authenticate(Socket, Password) of
                ok ->
                    {Socket, fun(Channel, Payload) ->
                        case redis_command(Socket, ["PUBLISH", Channel, Payload]) of
                            {ok, _} -> ok;
                            {error, Reason} ->
                                logger:warning("Calls Redis publish failed: ~p", [Reason]),
                                _ = timer:send_after(250, reconnect),
                                ok
                        end
                    end};
                {error, _} ->
                    _ = gen_tcp:close(Socket),
                    _ = timer:send_after(250, reconnect),
                    {undefined, fun(_, _) -> ok end}
            end;
        {error, Reason} ->
            logger:warning("Calls Redis connection failed: ~p", [Reason]),
            _ = timer:send_after(250, reconnect),
            {undefined, fun(_, _) -> ok end}
    end.

-spec authenticate(gen_tcp:socket(), string()) -> ok | {error, unavailable}.
authenticate(_Socket, "") -> ok;
authenticate(Socket, Password) ->
    case redis_command(Socket, ["AUTH", Password]) of
        {ok, Response} ->
            case Response of
                <<"+OK", _/binary>> -> ok;
                _ -> {error, unavailable}
            end;
        Error -> Error
    end.

-spec redis_command(gen_tcp:socket(), [term()]) -> {ok, binary()} | {error, unavailable}.
redis_command(Socket, Parts) ->
    Command = [<<$*>>, integer_to_binary(length(Parts)), <<"\r\n">>,
               [[<<$$>>, integer_to_binary(byte_size(iolist_to_binary([Part]))), <<"\r\n">>, Part, <<"\r\n">>] || Part <- Parts]],
    case gen_tcp:send(Socket, Command) of
        ok ->
            case gen_tcp:recv(Socket, 0, 1000) of
                {ok, <<$-, _/binary>>} -> {error, unavailable};
                {ok, Response} -> {ok, Response};
                {error, _} -> {error, unavailable}
            end;
        {error, _} -> {error, unavailable}
    end.

-spec publish([binary()], event()) -> ok.
publish(Users, Event) ->
    Payload = iolist_to_binary(json:encode(Event)),
    [publish_one(User, Payload) || User <- lists:usort(Users)],
    ok.

-spec publish_one(binary(), binary()) -> ok.
publish_one(User, Payload) -> publish_to(<<"chat:user:", User/binary>>, Payload).

-spec publish_to(binary(), binary()) -> ok.
publish_to(Channel, Payload) ->
    case whereis(?MODULE) of
        undefined -> ok;
        _ -> gen_server:cast(?MODULE, {publish, Channel, Payload})
    end.

-spec kind_binary(atom()) -> binary().
kind_binary(end_of_candidates) -> <<"end-of-candidates">>;
kind_binary(Kind) -> atom_to_binary(Kind).
