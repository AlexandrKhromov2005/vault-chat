-module(vault_calls_recovery).
-moduledoc "Starts every unfinished database-backed call during application startup.".
-behaviour(gen_server).

-export([start_link/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2]).

-spec start_link() -> gen_server:start_ret().
start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

-spec init([]) -> {ok, []}.
init([]) ->
    {ok, Ids} = vault_calls_store:list_active(vault_calls_store),
    _ = [{ok, _} = vault_call_sup:start_existing(#{id => Id}) || Id <- Ids],
    _ = vault_calls_store:purge_expired(vault_calls_store),
    _ = timer:send_interval(3600000, purge_expired),
    {ok, []}.

-spec handle_call(term(), gen_server:from(), []) -> {reply, {error, badarg}, []}.
handle_call(_Request, _From, State) ->
    {reply, {error, badarg}, State}.

-spec handle_cast(term(), []) -> {noreply, []}.
handle_cast(_Request, State) ->
    {noreply, State}.

-spec handle_info(term(), []) -> {noreply, []}.
handle_info(purge_expired, State) ->
    _ = vault_calls_store:purge_expired(vault_calls_store),
    {noreply, State};
handle_info(_Message, State) ->
    {noreply, State}.
