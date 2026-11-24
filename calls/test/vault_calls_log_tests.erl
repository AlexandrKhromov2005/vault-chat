-module(vault_calls_log_tests).

-include_lib("eunit/include/eunit.hrl").

json_log_test() ->
    Output = format({string, "Calls started"}),
    #{<<"level">> := <<"info">>, <<"msg">> := <<"Calls started">>, <<"time">> := Time} = json:decode(Output),
    ?assert(is_binary(Time)),
    ?assertEqual($\n, lists:last(binary_to_list(Output))).

report_body_is_suppressed_test() ->
    Output = format({report, #{payload => <<"synthetic-sensitive-sdp">>}}),
    ?assertNotEqual(nomatch, binary:match(Output, <<"report body suppressed">>)),
    ?assertEqual(nomatch, binary:match(Output, <<"synthetic-sensitive-sdp">>)).

format(Message) ->
    iolist_to_binary(vault_calls_log:format(#{level => info, msg => Message,
        meta => #{time => 0, pid => self(), application => vault_calls}}, #{})).
