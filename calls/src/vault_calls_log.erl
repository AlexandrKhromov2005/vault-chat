-module(vault_calls_log).
-moduledoc "Project JSON logs without report bodies that can contain signaling data.".

-export([install/0, format/2]).

-spec install() -> ok.
install() ->
    Config = #{config => #{type => standard_io}, formatter => {?MODULE, #{}}},
    case logger:add_handler(vault_calls_json, logger_std_h, Config) of
        ok -> ok = logger:remove_handler(default);
        {error, {already_exist, _}} -> ok = logger:remove_handler(default)
    end.

-spec format(logger:log_event(), logger:formatter_config()) -> unicode:chardata().
format(#{level := Level, msg := Message, meta := Meta}, _Config) ->
    Time = maps:get(time, Meta, erlang:system_time(microsecond)),
    Body = #{time => binary_value(calendar:system_time_to_rfc3339(Time, [{unit, microsecond}, {offset, "Z"}])),
             level => atom_to_binary(Level), msg => message(Message),
             application => atom_to_binary(maps:get(application, Meta, vault_calls)),
             pid => binary_value(io_lib:format("~p", [maps:get(pid, Meta, self())]))},
    [json:encode(Body), "\n"].

-spec message(term()) -> binary().
message({string, String}) -> binary_value(String);
message({report, _Report}) -> <<"report body suppressed">>;
message({Format, Args}) -> binary_value(io_lib:format(Format, Args)).

-spec binary_value(term()) -> binary().
binary_value(Value) ->
    case unicode:characters_to_binary(Value) of
        Binary when is_binary(Binary) -> Binary;
        _ -> <<"invalid unicode">>
    end.
