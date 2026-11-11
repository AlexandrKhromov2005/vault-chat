-module(vault_calls_app).
-moduledoc "The Calls application: starts the supervision tree.".
-behaviour(application).

-export([start/2, stop/1]).

-spec start(application:start_type(), term()) -> {ok, pid()} | {error, term()}.
start(_Type, _Args) ->
    case vault_calls_sup:start_link() of
        {ok, Pid} -> {ok, Pid};
        ignore -> {error, ignore};
        {error, _} = Error -> Error
    end.

-spec stop(term()) -> ok.
stop(_State) ->
    ok.
