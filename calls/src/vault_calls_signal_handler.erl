-module(vault_calls_signal_handler).
-moduledoc "SIGTERM drain before the normal OTP shutdown.".
-behaviour(gen_event).

-export([install/0]).
-export([init/1, handle_event/2, handle_call/2, handle_info/2, terminate/2, code_change/3]).

-spec install() -> ok.
install() ->
    _ = gen_event:delete_handler(erl_signal_server, erl_signal_handler, []),
    case gen_event:add_handler(erl_signal_server, ?MODULE, []) of
        ok -> ok;
        {'EXIT', _} -> ok;
        {error, already_present} -> ok
    end.

-spec init(list()) -> {ok, map()}.
init([]) -> {ok, #{}}.

-spec handle_event(atom(), map()) -> {ok, map()}.
handle_event(sigterm, State) ->
    _ = spawn(fun() ->
        ok = vault_calls_metrics:drain(),
        init:stop()
    end),
    {ok, State};
handle_event(sigusr1, State) ->
    erlang:halt("Received SIGUSR1"),
    {ok, State};
handle_event(sigquit, State) ->
    erlang:halt(),
    {ok, State};
handle_event(_Signal, State) -> {ok, State}.

-spec handle_call(term(), map()) -> {ok, ok, map()}.
handle_call(_Request, State) -> {ok, ok, State}.

-spec handle_info(term(), map()) -> {ok, map()}.
handle_info(_Message, State) -> {ok, State}.

-spec terminate(term(), map()) -> ok.
terminate(_Reason, _State) -> ok.

-spec code_change(term(), map(), term()) -> {ok, map()}.
code_change(_OldVersion, State, _Extra) -> {ok, State}.
