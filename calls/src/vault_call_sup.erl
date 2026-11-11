-module(vault_call_sup).
-moduledoc """
Supervisor of call processes, one per call. A crash of one call never
touches the others. An abnormal exit restarts the call from its database row;
a normally ended call remains ended.
""".
-behaviour(supervisor).

-export([start_link/0, start_call/1, start_existing/1, init/1]).

-spec start_link() -> supervisor:startlink_ret().
start_link() ->
    supervisor:start_link({local, ?MODULE}, ?MODULE, []).

-spec start_call(vault_call:start_args()) -> supervisor:startchild_ret() | {ok, replay} | vault_calls_store:create_reply().
start_call(#{id := Id} = Args) ->
    case vault_calls_store:create_call(vault_calls_store, Args) of
        {ok, _} ->
            case start_existing(#{id => Id}) of
                {error, not_found} -> {ok, replay};
                {error, {not_found, _}} -> {ok, replay};
                Result -> Result
            end;
        {error, _} = Error -> Error
    end.

-spec start_existing(#{id := binary()}) -> supervisor:startchild_ret().
start_existing(Args) ->
    case supervisor:start_child(?MODULE, [Args]) of
        {error, {already_started, Pid}} -> {ok, Pid};
        Result -> Result
    end.

-spec init([]) -> {ok, {supervisor:sup_flags(), [supervisor:child_spec()]}}.
init([]) ->
    Flags = #{strategy => simple_one_for_one, intensity => 100, period => 10},
    Call = #{id => vault_call, start => {vault_call, start_link, []}, restart => transient,
             shutdown => 5000},
    {ok, {Flags, [Call]}}.
