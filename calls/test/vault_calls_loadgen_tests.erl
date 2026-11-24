-module(vault_calls_loadgen_tests).

-include_lib("eunit/include/eunit.hrl").

absolute_rss_gate_test() ->
    ?assertEqual(pass, vault_calls_loadgen:gate_status([1000, 2000], 0, 20000000, 30000000)),
    ?assertEqual(absolute_rss_exceeded,
                 vault_calls_loadgen:gate_status([1000], 0, 200000000, 268435457)),
    ?assertEqual(rss_growth_exceeded,
                 vault_calls_loadgen:gate_status([1000], 0, 10000000, 300000000)).

only_pass_opens_gate_test() ->
    ?assertEqual(true, vault_calls_loadgen:gates(#{<<"gate_status">> => <<"pass">>})),
    ?assertEqual(false, vault_calls_loadgen:gates(#{<<"gate_status">> => <<"absolute_rss_exceeded">>})),
    ?assertEqual(false, vault_calls_loadgen:gates(#{<<"gate_status">> => <<"rss_growth_exceeded">>})),
    ?assertEqual(false, vault_calls_loadgen:gates(#{<<"gate_status">> => <<"latency_failed">>})).

latency_and_errors_still_fail_test() ->
    ?assertEqual(latency_failed, vault_calls_loadgen:gate_status([200001], 0, 0, 0)),
    ?assertEqual(latency_failed, vault_calls_loadgen:gate_status([1000], 1, 0, 0)).
