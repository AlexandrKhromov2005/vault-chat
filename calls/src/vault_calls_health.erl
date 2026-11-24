-module(vault_calls_health).
-moduledoc "gRPC health reflects the Calls drain state.".

-export([check/2, watch/2]).

-spec check(ctx:t(), map()) -> {ok, map(), ctx:t()}.
check(Ctx, #{service := <<>>}) ->
    Status = case vault_calls_metrics:draining() of
        true -> 'NOT_SERVING';
        false -> 'SERVING'
    end,
    {ok, #{status => Status}, Ctx};
check(Ctx, #{service := _Service}) -> {ok, #{status => 'UNKNOWN'}, Ctx}.

-spec watch(term(), term()) -> ok.
watch(_Request, _Stream) -> ok.
