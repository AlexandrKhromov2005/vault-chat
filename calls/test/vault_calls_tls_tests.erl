-module(vault_calls_tls_tests).

-include_lib("eunit/include/eunit.hrl").
-include_lib("public_key/include/public_key.hrl").

identity_test() ->
    ?assertEqual(gateway, identity("gateway.crt")),
    ?assertEqual(auth, identity("auth.crt")),
    ?assertEqual(undefined, vault_calls_tls:identity(<<"not a certificate">>)),
    Decoded = public_key:pkix_decode_cert(der("gateway.crt"), otp),
    ?assertEqual(gateway, vault_calls_tls:identity(Decoded)),
    #'OTPCertificate'{tbsCertificate = #'OTPTBSCertificate'{extensions = Extensions}} = Decoded,
    Extension = lists:keyfind(?'id-ce-subjectAltName', #'Extension'.extnID, Extensions),
    ?assertEqual({valid, gateway},
        vault_calls_tls:verify_peer(Decoded, {extension, Extension}, gateway)).

gateway_acl_test() ->
    ?assertEqual({true, "gateway"}, vault_calls_tls:gateway_auth(der("gateway.crt"))),
    ?assertEqual(false, vault_calls_tls:gateway_auth(der("auth.crt"))).

tls_versions_test() ->
    Client = vault_calls_tls:client_options(auth, "/unused"),
    Server = vault_calls_tls:server_options("/unused"),
    ?assertEqual(['tlsv1.3'], proplists:get_value(versions, Client)),
    ?assertEqual([<<"h2">>], proplists:get_value(alpn_advertised_protocols, Client)),
    ?assertEqual(false, lists:keyfind(client_preferred_next_protocols, 1, Client)),
    ?assertEqual(false, lists:keyfind(reuse_sessions, 1, Client)),
    ?assertEqual(['tlsv1.3'], proplists:get_value(versions, Server)),
    ?assertEqual(false, lists:keyfind(reuse_sessions, 1, Server)).

server_alpn_test() ->
    {ok, {_, [#{start := {grpcbox_acceptor, {ssl, Server, _, Options}, []}}]}} =
        vault_calls_pool:init([#{max_concurrent_streams => 10}, #{},
            #{ssl => true, versions => ['tlsv1.3']}]),
    ?assertMatch([_ | _], chatterbox_h2_settings:to_proplist(Server)),
    ?assertEqual([<<"h2">>], proplists:get_value(alpn_preferred_protocols, Options)),
    ?assertEqual(false, lists:keyfind(next_protocols_advertised, 1, Options)).

identity(Name) ->
    case der(Name) of
        Cert -> vault_calls_tls:identity(Cert)
    end.

der(Name) ->
    Path = filename:join([code:lib_dir(vault_calls), "test", "fixtures", Name]),
    {ok, Pem} = file:read_file(Path),
    [{'Certificate', Cert, not_encrypted}] = public_key:pem_decode(Pem),
    Cert.
