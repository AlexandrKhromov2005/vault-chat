#!/bin/sh
# Explicit opt-in, safe leaf rotation, and no automatic plaintext rollback.
set -eu
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
case "${1:-status}" in
    init) python3 "$VAULT_ROOT/scripts/gen-rpc-certs.py" "$VAULT_ROOT/secrets";;
    enable|rotate)
        action=$1
        if [ "$action" = rotate ] && [ ! -f "$VAULT_ROOT/.env.rpc" ]; then
            echo 'Enable mTLS before rotating leaves' >&2; exit 1
        fi
        if [ "$action" = enable ]; then python3 "$VAULT_ROOT/scripts/gen-rpc-certs.py" "$VAULT_ROOT/secrets"; fi
        # Old images must reject the new flag; never report plaintext binaries as mTLS.
        for service in auth chat media gateway; do
            docker run --rm --network none --entrypoint /usr/local/bin/healthcheck \
                -e RPC_TLS_MODE=mtls -e RPC_TLS_SERVICE="$service" -e RPC_TLS_DIR=/run/secrets/rpc \
                -v "$VAULT_ROOT/secrets/rpc/$service:/run/secrets/rpc:ro" \
                "vault-chat/$service:$(value VAULT_IMAGE_TAG)" -check-rpc-tls
        done
        "$VAULT_ROOT/scripts/vps.sh" backup
        if [ "$action" = rotate ]; then
            compose stop gateway media chat auth
            python3 "$VAULT_ROOT/scripts/gen-rpc-certs.py" "$VAULT_ROOT/secrets" --rotate
        fi
        printf 'RPC_TLS_MODE=mtls\n' > "$VAULT_ROOT/.env.rpc"
        chmod 600 "$VAULT_ROOT/.env.rpc"
        compose up -d --no-build --force-recreate --wait auth chat media gateway
        probe_ready
        echo 'mTLS enabled; service identities and user authorization are both checked'
        ;;
    status)
        if [ -f "$VAULT_ROOT/.env.rpc" ]; then
            echo 'mTLS overlay enabled'
            for service in auth chat media gateway; do
                printf '%s: ' "$service"
                openssl x509 -in "$VAULT_ROOT/secrets/rpc/$service/tls.crt" -noout -enddate
            done
        else echo 'mTLS overlay disabled'; fi
        ;;
    *) echo 'Usage: scripts/vps.sh mtls init|enable|rotate|status' >&2; exit 2;;
esac
