-module(vault_calls_socket_tests).

-include_lib("eunit/include/eunit.hrl").

listener_starts_test() ->
    Pool = vault_calls_test_pool,
    {ok, PoolPid} = vault_calls_pool:start_link(Pool, #{}, #{}, #{ssl => false}),
    {ok, SocketPid} = vault_calls_socket:start_link(Pool,
        #{port => 0, ip => {127, 0, 0, 1}}, #{size => 1}),
    ok = gen_server:stop(SocketPid),
    ok = gen_server:stop(PoolPid).
