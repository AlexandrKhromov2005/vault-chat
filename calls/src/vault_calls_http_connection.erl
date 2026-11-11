-module(vault_calls_http_connection).
-moduledoc "One bounded HTTP/1.1 connection for the loopback prototype.".

-export([serve/1]).

-spec serve(gen_tcp:socket()) -> ok.
serve(Socket) ->
    Result = try request(Socket) of
        {ok, Method, Uri, Headers, Body} ->
            Env = [{request_method, Method}, {script_name, Uri} | authorization(Headers)],
            try vault_calls_http:route(Env, Body) of
                RouteResult -> RouteResult
            catch
                _:_ -> {500, #{<<"error">> => <<"internal">>}, []}
            end;
        {error, Reason} -> error_result(Reason)
    catch
        _:_ -> {500, #{<<"error">> => <<"internal">>}, []}
    end,
    respond(Socket, Result),
    gen_tcp:close(Socket),
    ok.

-spec request(gen_tcp:socket()) -> {ok, binary(), binary(), map(), binary()} | {error, badarg | too_large}.
request(Socket) ->
    read_headers(Socket, <<>>).

-spec read_headers(gen_tcp:socket(), binary()) ->
    {ok, binary(), binary(), map(), binary()} | {error, badarg | too_large}.
read_headers(Socket, Buffer) ->
    case binary:match(Buffer, <<"\r\n\r\n">>) of
        {Position, 4} ->
            HeaderBlock = binary:part(Buffer, 0, Position),
            Rest = binary:part(Buffer, Position + 4, byte_size(Buffer) - Position - 4),
            parse_headers(Socket, HeaderBlock, Rest);
        nomatch when byte_size(Buffer) > 8192 -> {error, too_large};
        nomatch ->
            case gen_tcp:recv(Socket, 0, 5000) of
                {ok, Chunk} -> read_headers(Socket, <<Buffer/binary, Chunk/binary>>);
                {error, _} -> {error, badarg}
            end
    end.

-spec parse_headers(gen_tcp:socket(), binary(), binary()) ->
    {ok, binary(), binary(), map(), binary()} | {error, badarg | too_large}.
parse_headers(Socket, HeaderBlock, Rest) ->
    case binary:split(HeaderBlock, <<"\r\n">>, [global]) of
        [RequestLine | Lines] when length(Lines) =< 32 ->
            case binary:split(RequestLine, <<" ">>, [global]) of
                [Method, Uri, _Version] when byte_size(Uri) =< 4096 ->
                    case header_map(Lines, #{}) of
                        {ok, Headers} -> body(Socket, Rest, Headers, Method, Uri);
                        error -> {error, badarg}
                    end;
                _ -> {error, badarg}
            end;
        _ -> {error, badarg}
    end.

-spec header_map([binary()], map()) -> {ok, map()} | error.
header_map([], Headers) ->
    {ok, Headers};
header_map([Line | Rest], Headers) ->
    case binary:split(Line, <<":">>) of
        [Name, Value] -> header_map(Rest, maps:put(header_name(Name), trim(Value), Headers));
        _ -> error
    end.

-spec body(gen_tcp:socket(), binary(), map(), binary(), binary()) ->
    {ok, binary(), binary(), map(), binary()} | {error, badarg | too_large}.
body(Socket, Rest, Headers, Method, Uri) ->
    case {maps:get(<<"transfer-encoding">>, Headers, undefined), content_length(Headers)} of
        {undefined, {ok, Length}} ->
            read_body(Socket, Rest, Length, Method, Uri, Headers);
        {undefined, {error, Reason}} -> {error, Reason};
        _ -> {error, badarg}
    end.

-spec read_body(gen_tcp:socket(), binary(), non_neg_integer(), binary(), binary(), map()) ->
    {ok, binary(), binary(), map(), binary()} | {error, badarg | too_large}.
read_body(_Socket, Rest, Length, Method, Uri, Headers) when byte_size(Rest) >= Length ->
    {ok, Method, Uri, Headers, binary:part(Rest, 0, Length)};
read_body(Socket, Rest, Length, Method, Uri, Headers) ->
    receive_data(Socket, Rest, Length, Method, Uri, Headers).

-spec receive_data(gen_tcp:socket(), binary(), non_neg_integer(), binary(), binary(), map()) ->
    {ok, binary(), binary(), map(), binary()} | {error, badarg | too_large}.
receive_data(Socket, Rest, Length, Method, Uri, Headers) ->
    case gen_tcp:recv(Socket, Length - byte_size(Rest), 5000) of
        {ok, Chunk} -> {ok, Method, Uri, Headers, <<Rest/binary, Chunk/binary>>};
        {error, _} -> {error, badarg}
    end.

-spec content_length(map()) -> {ok, non_neg_integer()} | {error, badarg | too_large}.
content_length(Headers) ->
    case maps:get(<<"content-length">>, Headers, <<"0">>) of
        Value ->
            try binary_to_integer(Value) of
                Length when Length >= 0, Length =< 16384 -> {ok, Length};
                Length when Length > 16384 -> {error, too_large};
                _ -> {error, badarg}
            catch
                _:_ -> {error, badarg}
            end
    end.

-spec header_name(binary()) -> binary().
header_name(Name) ->
    iolist_to_binary(string:lowercase(Name)).

-spec trim(binary()) -> binary().
trim(Value) ->
    iolist_to_binary(re:replace(Value, <<"^\\s+|\\s+$">>, <<>>, [global, {return, binary}])).

-spec authorization(map()) -> list().
authorization(Headers) ->
    case maps:get(<<"authorization">>, Headers, undefined) of
        undefined -> [];
        Value -> [{http_authorization, binary_to_list(Value)}]
    end.

-spec error_result(atom()) -> {non_neg_integer(), map(), iodata()}.
error_result(too_large) -> {413, #{<<"error">> => <<"body_too_large">>}, []};
error_result(_) -> {400, #{<<"error">> => <<"badarg">>}, []}.

-spec respond(gen_tcp:socket(), {non_neg_integer(), map(), iodata()}) -> ok.
respond(Socket, {Status, Body, Extra}) ->
    Json = iolist_to_binary(json:encode(Body)),
    StatusLine = string:substr(vault_calls_http:status_line(Status), 9),
    Response = ["HTTP/1.1 ", StatusLine,
                "\r\ncontent-type: application/json\r\ncontent-length: ", integer_to_list(byte_size(Json)),
                "\r\nconnection: close\r\n", Extra, "\r\n", Json],
    _ = gen_tcp:send(Socket, Response),
    ok.
