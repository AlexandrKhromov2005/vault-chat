-module(vault_calls_sup).
-moduledoc """
Top supervisor: the registry of calls and the supervisor of call processes.
The registry starts first, so a call can always register; if it dies, every
call restarts with it (rest_for_one), since their names are gone.
""".
-behaviour(supervisor).

-export([start_link/0, init/1]).

-spec start_link() -> supervisor:startlink_ret().
start_link() ->
    supervisor:start_link({local, ?MODULE}, ?MODULE, []).

-spec init([]) -> {ok, {supervisor:sup_flags(), [supervisor:child_spec()]}}.
init([]) ->
    Flags = #{strategy => rest_for_one, intensity => 5, period => 10},
    Children = [
        #{id => vault_calls_registry, start => {vault_calls_registry, start_link, []}},
        #{id => vault_call_sup, start => {vault_call_sup, start_link, []}, type => supervisor}
    ],
    {ok, {Flags, Children}}.
