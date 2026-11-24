#!/bin/sh
# Explicit opt-in, safe leaf rotation, and no automatic plaintext rollback.
set -eu
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
# The push broker serves with its own leaf and must be restarted with the
# others; it exists only with the push overlay.
leaves="auth chat media gateway calls"
if [ -f "$VAULT_ROOT/.env.push" ]; then leaves="$leaves nats"; fi
case "${1:-status}" in
    init) python3 "$VAULT_ROOT/scripts/gen-rpc-certs.py" "$VAULT_ROOT/secrets";;
    enable|rotate)
        action=$1
        if [ "$action" = rotate ] && [ ! -f "$VAULT_ROOT/.env.rpc" ]; then
            echo 'Enable mTLS before rotating leaves' >&2; exit 1
        fi
        if [ "$action" = enable ]; then python3 "$VAULT_ROOT/scripts/gen-rpc-certs.py" "$VAULT_ROOT/secrets"; fi
        # Old images must reject the new flag; never report plaintext binaries as mTLS.
        check_rpc_images
        "$VAULT_ROOT/scripts/vps.sh" backup
        if [ "$action" = rotate ]; then
            # shellcheck disable=SC2086
            compose stop $leaves
            python3 "$VAULT_ROOT/scripts/gen-rpc-certs.py" "$VAULT_ROOT/secrets" --rotate
        fi
        printf 'RPC_TLS_MODE=mtls\n' > "$VAULT_ROOT/.env.rpc"
        chmod 600 "$VAULT_ROOT/.env.rpc"
        # shellcheck disable=SC2086
        compose up -d --no-build --force-recreate --wait $leaves
        probe_ready
        echo 'mTLS enabled; service identities and user authorization are both checked'
        ;;
    status)
        if [ -f "$VAULT_ROOT/.env.rpc" ]; then
            echo 'mTLS overlay enabled'
            for service in $leaves; do
                printf '%s: ' "$service"
                openssl x509 -in "$VAULT_ROOT/secrets/rpc/$service/tls.crt" -noout -enddate
            done
        else echo 'mTLS overlay disabled'; fi
        ;;
    *) echo 'Usage: scripts/vps.sh mtls init|enable|rotate|status' >&2; exit 2;;
esac
