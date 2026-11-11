#!/usr/bin/env escript
%%! -noshell

main([Mode, Directory]) ->
    case Mode of
        "setup" -> vault_calls_loadgen:setup(Directory);
        "verify" -> vault_calls_loadgen:verify(Directory)
    end.
