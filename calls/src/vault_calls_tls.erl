-module(vault_calls_tls).
-moduledoc "Exact SPIFFE identity checks for Calls gRPC peers.".

-include_lib("public_key/include/public_key.hrl").

-export([client_options/2, server_options/1, gateway_auth/1, identity/1, verify_peer/3]).

-spec client_options(atom(), string()) -> list().
client_options(Target, Dir) ->
    [{verify, verify_peer}, {versions, ['tlsv1.3']}, {alpn_advertised_protocols, [<<"h2">>]},
     {cacertfile, filename:join(Dir, "ca.pem")},
     {certfile, filename:join(Dir, "tls.crt")}, {keyfile, filename:join(Dir, "tls.key")},
     {server_name_indication, atom_to_list(Target) ++ ".vault.internal"},
     {verify_fun, {fun verify_peer/3, Target}}, {depth, 3}].

-spec server_options(string()) -> list().
server_options(Dir) ->
    [{verify, verify_peer}, {fail_if_no_peer_cert, true}, {versions, ['tlsv1.3']},
     {cacertfile, filename:join(Dir, "ca.pem")}, {certfile, filename:join(Dir, "tls.crt")},
     {keyfile, filename:join(Dir, "tls.key")}, {verify_fun, {fun verify_peer/3, gateway}},
     {depth, 3}].

-spec gateway_auth(public_key:der_encoded() | #'OTPCertificate'{}) -> {true, string()} | false.
gateway_auth(Cert) ->
    case identity(Cert) of
        gateway -> {true, "gateway"};
        _ -> false
    end.

-spec verify_peer(public_key:der_encoded() | #'OTPCertificate'{},
    valid | valid_peer | {bad_cert, term()} | {extension, #'Extension'{}}, atom()) ->
    {valid, atom()} | {fail, atom()}.
verify_peer(_Cert, valid, Target) -> {valid, Target};
verify_peer(_Cert, {extension, _Extension}, Target) -> {valid, Target};
verify_peer(Cert, valid_peer, Target) ->
    case identity(Cert) of
        Target -> {valid, Target};
        _ -> {fail, unexpected_service_identity}
    end;
verify_peer(_Cert, {bad_cert, _}, _Target) -> {fail, invalid_certificate}.

-spec identity(public_key:der_encoded() | #'OTPCertificate'{}) -> atom() | undefined.
identity(#'OTPCertificate'{tbsCertificate = #'OTPTBSCertificate'{extensions = Extensions}}) ->
    case lists:keyfind(?'id-ce-subjectAltName', #'Extension'.extnID, Extensions) of
        #'Extension'{extnValue = Names} -> service_identity(Names);
        false -> undefined
    end;
identity(Cert) ->
    try public_key:pkix_decode_cert(Cert, otp) of
        Decoded -> identity(Decoded)
    catch
        _:_ -> undefined
    end.

-spec service_identity(term()) -> atom() | undefined.
service_identity(Names) ->
    Uris = [unicode:characters_to_binary(Uri) || {uniformResourceIdentifier, Uri} <- Names],
    case Uris of
        [Uri] ->
            Prefix = <<"spiffe://vault-chat/service/">>,
            case binary:longest_common_prefix([Uri, Prefix]) =:= byte_size(Prefix) of
                true -> known(binary:part(Uri, byte_size(Prefix), byte_size(Uri) - byte_size(Prefix)));
                false -> undefined
            end;
        _ -> undefined
    end.

-spec known(binary()) -> atom() | undefined.
known(<<"auth">>) -> auth;
known(<<"chat">>) -> chat;
known(<<"media">>) -> media;
known(<<"gateway">>) -> gateway;
known(<<"calls">>) -> calls;
known(<<"preview">>) -> preview;
known(_) -> undefined.
