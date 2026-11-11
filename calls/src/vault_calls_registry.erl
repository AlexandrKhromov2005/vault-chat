-module(vault_calls_registry).
-moduledoc """
Names call processes by call id, for `{via, vault_calls_registry, Id}`.
Names live in a protected ETS table written only by this process, which
monitors every registered process and removes its name when it exits.
""".
-behaviour(gen_server).

-export([start_link/0, register_name/2, unregister_name/1, whereis_name/1, send/2]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2]).

-define(TABLE, ?MODULE).

-spec start_link() -> gen_server:start_ret().
start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

-spec register_name(term(), pid()) -> yes | no.
register_name(Name, Pid) ->
    gen_server:call(?MODULE, {register, Name, Pid}).

-spec unregister_name(term()) -> ok.
unregister_name(Name) ->
    gen_server:call(?MODULE, {unregister, Name}).

-spec whereis_name(term()) -> pid() | undefined.
whereis_name(Name) ->
    case ets:lookup(?TABLE, Name) of
        [{Name, Pid}] -> Pid;
        [] -> undefined
    end.

-spec send(term(), term()) -> pid().
send(Name, Message) ->
    case whereis_name(Name) of
        undefined -> exit({badarg, {Name, Message}});
        Pid -> Pid ! Message, Pid
    end.

-spec init([]) -> {ok, #{reference() => term()}}.
init([]) ->
    _ = ets:new(?TABLE, [named_table, protected, {read_concurrency, true}]),
    {ok, #{}}.

-spec handle_call(term(), gen_server:from(), #{reference() => term()}) ->
    {reply, yes | no | ok, #{reference() => term()}}.
handle_call({register, Name, Pid}, _From, Monitors) ->
    case ets:lookup(?TABLE, Name) of
        [{Name, OldPid}] ->
            case is_process_alive(OldPid) of
                false -> true = ets:delete(?TABLE, Name);
                true -> ok
            end;
        [] -> ok
    end,
    case ets:insert_new(?TABLE, {Name, Pid}) of
        true -> {reply, yes, Monitors#{monitor(process, Pid) => Name}};
        false -> {reply, no, Monitors}
    end;
handle_call({unregister, Name}, _From, Monitors) ->
    true = ets:delete(?TABLE, Name),
    {reply, ok, Monitors}.

-spec handle_cast(term(), #{reference() => term()}) -> {noreply, #{reference() => term()}}.
handle_cast(_Message, Monitors) ->
    {noreply, Monitors}.

-spec handle_info(term(), #{reference() => term()}) -> {noreply, #{reference() => term()}}.
handle_info({'DOWN', Ref, process, Pid, _Reason}, Monitors) ->
    {Name, Rest} = maps:take(Ref, Monitors),
    true = ets:delete_object(?TABLE, {Name, Pid}),
    {noreply, Rest};
handle_info(_Message, Monitors) ->
    {noreply, Monitors}.
