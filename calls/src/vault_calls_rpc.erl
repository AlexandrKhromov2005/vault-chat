-module(vault_calls_rpc).
-moduledoc "Internal gRPC API of Calls. Auth and Chat are authoritative for every command.".
-compile({no_auto_import, [error/1]}).

-export([create/2, accept/2, reject/2, cancel/2, connected/2, hangup/2, heartbeat/2,
         signal/2, get/2, list_active/2, signals/2, turn/2]).

-type identity() :: {binary(), binary(), binary()}.
-type result() :: {ok, map(), ctx:t()} | {grpc_error, {binary(), binary()}}.

-spec create(ctx:t(), map()) -> result().
create(Ctx, Request) ->
    case vault_calls_metrics:draining() of
        true -> error(capacity);
        false -> create_request(Ctx, Request)
    end.

-spec create_request(ctx:t(), map()) -> result().
create_request(Ctx, Request) ->
    with_identity(Ctx, Request, fun({User, Device, Token}) ->
        case {binary_field(Request, room_id, 128), binary_field(Request, command_id, 128)} of
            {{ok, RoomId}, {ok, CommandId}} ->
                case (chat_mod()):get_room(RoomId, User, Token) of
                    {ok, Callee} ->
                        Fingerprint = fingerprint(create, RoomId, CommandId, User, Device),
                        Id = binary:encode_hex(crypto:hash(sha256, [User, RoomId, CommandId, Fingerprint])),
                        Args = #{id => Id, room_id => RoomId, caller => User, caller_device => Device,
                                 callee => Callee, command_id => CommandId, fingerprint => Fingerprint},
                        case vault_call_sup:start_call(Args) of
                            {ok, _} ->
                                ok = vault_calls_events:call_changed(Args#{state => ringing, state_version => 0}),
                                {ok, #{call => public_call(Args, ringing)}, Ctx};
                            {error, Reason} -> error(Reason)
                        end;
                    {error, Reason} -> error(Reason)
                end;
            _ -> error(badarg)
        end
    end).

-spec accept(ctx:t(), map()) -> result().
accept(Ctx, Request) -> command(Ctx, Request, accept).

-spec reject(ctx:t(), map()) -> result().
reject(Ctx, Request) -> command(Ctx, Request, reject).

-spec cancel(ctx:t(), map()) -> result().
cancel(Ctx, Request) -> command(Ctx, Request, cancel).

-spec connected(ctx:t(), map()) -> result().
connected(Ctx, Request) -> command(Ctx, Request, connected).

-spec hangup(ctx:t(), map()) -> result().
hangup(Ctx, Request) -> command(Ctx, Request, 'end').

-spec heartbeat(ctx:t(), map()) -> result().
heartbeat(Ctx, Request) -> command(Ctx, Request, heartbeat).

-spec signal(ctx:t(), map()) -> result().
signal(Ctx, Request) ->
    with_identity(Ctx, Request, fun({User, Device, _Token} = Identity) ->
        Id = maps:get(call_id, Request, <<>>),
        with_call(Id, Identity, fun(_Data) ->
            case {kind(Request), maps:get(payload, Request, undefined), maps:get(sender_seq, Request, undefined)} of
                {{ok, Kind}, Payload, SenderSeq} when is_binary(Payload), is_integer(SenderSeq), SenderSeq > 0 ->
                    case vault_call:signal(Id, User, Device, Kind, Payload, SenderSeq) of
                        {ok, State, SignalSeq} ->
                            {ok, #{state => atom_to_binary(State), signal_seq => SignalSeq}, Ctx};
                        {error, Reason} -> error(Reason)
                    end;
                _ -> error(badarg)
            end
        end)
    end).

-spec get(ctx:t(), map()) -> result().
get(Ctx, Request) ->
    with_identity(Ctx, Request, fun(Identity) ->
        Id = maps:get(call_id, Request, <<>>),
        with_call(Id, Identity, fun(Data) -> {ok, #{call => public_call(Data)}, Ctx} end)
    end).

-spec list_active(ctx:t(), map()) -> result().
list_active(Ctx, Request) ->
    with_identity(Ctx, Request, fun({User, _Device, _Token}) ->
        case vault_calls_store:list_active(vault_calls_store) of
            {ok, Ids} ->
                Calls = lists:filtermap(fun(Id) ->
                    case vault_calls_store:get_call(vault_calls_store, Id) of
                        {ok, #{caller := User} = Data} -> {true, public_call(Data)};
                        {ok, #{callee := User} = Data} -> {true, public_call(Data)};
                        _ -> false
                    end
                end, Ids),
                {ok, #{calls => Calls}, Ctx};
            {error, Reason} -> error(Reason)
        end
    end).

-spec signals(ctx:t(), map()) -> result().
signals(Ctx, Request) ->
    with_identity(Ctx, Request, fun(Identity) ->
        Id = maps:get(call_id, Request, <<>>),
        with_call(Id, Identity, fun(_Data) ->
            case maps:get('after', Request, 0) of
                After when is_integer(After), After >= 0 ->
                    case vault_calls_store:signals_after(vault_calls_store, Id, After) of
                        {ok, Saved} -> {ok, #{signals => [public_signal(S) || S <- Saved]}, Ctx};
                        {error, Reason} -> error(Reason)
                    end;
                _ -> error(badarg)
            end
        end)
    end).

-spec turn(ctx:t(), map()) -> result().
turn(Ctx, Request) ->
    with_identity(Ctx, Request, fun({User, _Device, _Token} = Identity) ->
        with_call(maps:get(call_id, Request, <<>>), Identity, fun(Data) ->
            case maps:get(state, Data) of
                connecting -> turn_response(Ctx, Data, User);
                active -> turn_response(Ctx, Data, User);
                _ -> error(conflict)
            end
        end)
    end).

-spec turn_response(ctx:t(), map(), binary()) -> result().
turn_response(Ctx, Data, User) ->
    case vault_calls_turn:issue(maps:get(id, Data), User) of
        {ok, Response} -> {ok, Response, Ctx};
        {error, Reason} -> error(Reason)
    end.

-spec command(ctx:t(), map(), atom()) -> result().
command(Ctx, Request, Name) ->
    with_identity(Ctx, Request, fun({User, Device, _Token} = Identity) ->
        Id = maps:get(call_id, Request, <<>>),
        case binary_field(Request, command_id, 128) of
            {ok, CommandId} ->
                with_call(Id, Identity, fun(Data) ->
                    Meta = #{device => Device, command_id => CommandId,
                             fingerprint => fingerprint(Name, Id, CommandId, User, Device)},
                    case vault_call:command(Id, {Name, User, Device}, Meta) of
                        {ok, State} -> {ok, #{call => public_call(Data, State)}, Ctx};
                        {error, Reason} -> error(Reason)
                    end
                end);
            error -> error(badarg)
        end
    end).

-spec with_identity(ctx:t(), map(), fun((identity()) -> result())) -> result().
with_identity(_Ctx, Request, Fun) ->
    Identity = maps:get(identity, Request, #{}),
    Token = maps:get(access_token, Identity, <<>>),
    ExpectedUser = maps:get(user_id, Identity, <<>>),
    ExpectedDevice = maps:get(session_id, Identity, <<>>),
    case (auth_mod()):validate(Token) of
        {ok, {ExpectedUser, ExpectedDevice}} when byte_size(ExpectedUser) > 0, byte_size(ExpectedDevice) > 0 ->
            Fun({ExpectedUser, ExpectedDevice, Token});
        {ok, _} -> error(unauthorized);
        {error, Reason} -> error(Reason)
    end.

-spec with_call(binary(), identity(), fun((map()) -> result())) -> result().
with_call(Id, {User, _Device, Token}, Fun) ->
    case authorized_call(Id, User) of
        {ok, Data} ->
            case (chat_mod()):get_room(maps:get(room_id, Data), User, Token) of
                {ok, _} -> Fun(Data);
                {error, Reason} -> error(Reason)
            end;
        {error, Reason} -> error(Reason)
    end.

-spec authorized_call(binary(), binary()) -> {ok, map()} | {error, not_found | unavailable | badarg}.
authorized_call(Id, User) ->
    case vault_calls_store:get_call(vault_calls_store, Id) of
        {ok, #{caller := User} = Data} -> {ok, Data};
        {ok, #{callee := User} = Data} -> {ok, Data};
        {ok, _} -> {error, not_found};
        {error, Reason} -> {error, Reason}
    end.

-spec public_call(map()) -> map().
public_call(Data) -> public_call(Data, maps:get(state, Data)).

-spec public_call(map(), atom()) -> map().
public_call(Data, State) ->
    Version = maps:get(state_version, Data, 0),
    NextVersion = case maps:get(state, Data, State) of
        State -> Version;
        _ -> Version + 1
    end,
    #{call_id => maps:get(id, Data), room_id => maps:get(room_id, Data),
      state => atom_to_binary(State), state_version => NextVersion,
      caller_id => maps:get(caller, Data), callee_id => maps:get(callee, Data)}.

-spec public_signal(map()) -> map().
public_signal(Signal) ->
    #{signal_seq => maps:get(signal_seq, Signal), sender_device => maps:get(sender_device, Signal),
      sender_seq => maps:get(sender_seq, Signal), kind => kind_binary(maps:get(kind, Signal)),
      payload => maps:get(payload, Signal)}.

-spec fingerprint(atom(), binary(), binary(), binary(), binary()) -> binary().
fingerprint(Action, Target, CommandId, User, Device) ->
    crypto:hash(sha256, term_to_binary({Action, Target, CommandId, User, Device})).

-spec binary_field(map(), atom(), pos_integer()) -> {ok, binary()} | error.
binary_field(Map, Key, Limit) ->
    case maps:get(Key, Map, undefined) of
        Value when is_binary(Value), byte_size(Value) > 0, byte_size(Value) =< Limit -> {ok, Value};
        _ -> error
    end.

-spec kind(map()) -> {ok, atom()} | error.
kind(Request) ->
    case maps:get(kind, Request, undefined) of
        <<"offer">> -> {ok, offer};
        <<"answer">> -> {ok, answer};
        <<"candidate">> -> {ok, candidate};
        <<"end-of-candidates">> -> {ok, end_of_candidates};
        _ -> error
    end.

-spec kind_binary(atom()) -> binary().
kind_binary(end_of_candidates) -> <<"end-of-candidates">>;
kind_binary(Kind) -> atom_to_binary(Kind).

-spec auth_mod() -> module().
auth_mod() -> application:get_env(vault_calls, auth_mod, vault_calls_auth_stub).

-spec chat_mod() -> module().
chat_mod() -> application:get_env(vault_calls, chat_mod, vault_calls_chat_stub).

-spec error(atom()) -> {grpc_error, {binary(), binary()}}.
error(badarg) -> grpc_error(3, badarg);
error(unauthorized) -> grpc_error(16, unauthorized);
error(not_found) -> grpc_error(5, not_found);
error(busy) -> grpc_error(9, busy);
error(conflict) -> grpc_error(9, conflict);
error(seq_reused) -> grpc_error(9, seq_reused);
error(signal_limit) -> grpc_error(9, signal_limit);
error(candidate_limit) -> grpc_error(9, candidate_limit);
error(command_id_reused) -> grpc_error(9, command_id_reused);
error(signal_rate_limited) -> grpc_error(8, signal_rate_limited);
error(capacity) -> grpc_error(14, capacity);
error(unavailable) -> grpc_error(14, unavailable);
error(_) -> grpc_error(13, internal).

-spec grpc_error(non_neg_integer(), atom()) -> {grpc_error, {binary(), binary()}}.
grpc_error(Code, Reason) ->
    {grpc_error, {integer_to_binary(Code), atom_to_binary(Reason)}}.
