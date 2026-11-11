-module(vault_calls_http).
-moduledoc "Loopback HTTP prototype for the Calls control plane.".
-behaviour(gen_server).

-export([start_link/0, port/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2]).

-spec start_link() -> gen_server:start_ret().
start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

-spec port() -> non_neg_integer().
port() ->
    gen_server:call(?MODULE, port, 1000).

-spec init([]) -> {ok, []}.
init([]) ->
    {ok, []}.

-spec handle_call(term(), gen_server:from(), []) -> {reply, term(), []}.
handle_call(port, _From, State) ->
    {reply, 0, State};
handle_call(_Request, _From, State) ->
    {reply, {error, badarg}, State}.

-spec handle_cast(term(), []) -> {noreply, []}.
handle_cast(_Request, State) ->
    {noreply, State}.

-spec handle_info(term(), []) -> {noreply, []}.
handle_info(_Message, State) ->
    {noreply, State}.
