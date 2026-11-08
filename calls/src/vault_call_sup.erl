-module(vault_call_sup).
-moduledoc """
Supervisor of call processes, one per call. A crash of one call never
touches the others. Calls are temporary at this stage: their state lives
only in the process, so restarting one would forget it. Restarting from the
call's database row comes with persistence.
""".
-behaviour(supervisor).

-export([start_link/0, start_call/1, init/1]).

-spec start_link() -> supervisor:startlink_ret().
start_link() ->
    supervisor:start_link({local, ?MODULE}, ?MODULE, []).

-spec start_call(vault_call:start_args()) -> supervisor:startchild_ret().
start_call(Args) ->
    supervisor:start_child(?MODULE, [Args]).

-spec init([]) -> {ok, {supervisor:sup_flags(), [supervisor:child_spec()]}}.
init([]) ->
    Flags = #{strategy => simple_one_for_one, intensity => 100, period => 10},
    Call = #{id => vault_call, start => {vault_call, start_link, []}, restart => temporary,
             shutdown => 5000},
    {ok, {Flags, [Call]}}.
