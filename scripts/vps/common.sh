# Shared by the VPS commands; source after setting -eu.
VAULT_ROOT=${VAULT_ROOT:-/opt/vault-chat}
VAULT_PROJECT=${VAULT_PROJECT:-vault-chat}
case "$VAULT_PROJECT" in ''|*[!a-z0-9_-]*) echo 'Invalid VAULT_PROJECT' >&2; exit 1;; esac
export VAULT_ROOT VAULT_PROJECT
compose() {
    if [ -f "$VAULT_ROOT/.env.rpc" ]; then
        set -- -f "$VAULT_ROOT/deployments/docker-compose.mtls.yml" "$@"
    fi
    if [ -f "$VAULT_ROOT/.env.monitoring" ]; then
        set -- --env-file "$VAULT_ROOT/.env.monitoring" -f "$VAULT_ROOT/deployments/monitoring/app.yml" "$@"
    fi
    docker compose -p "$VAULT_PROJECT" --env-file "$VAULT_ROOT/.env.vps" \
        -f "$VAULT_ROOT/deployments/docker-compose.vps.yml" "$@"
}

monitor_compose() {
    docker compose -p "$VAULT_PROJECT-monitoring" --env-file "$VAULT_ROOT/.env.vps" --env-file "$VAULT_ROOT/.env.monitoring" \
        -f "$VAULT_ROOT/deployments/docker-compose.monitoring.yml" "$@"
}

root_only() {
    [ "$(id -u)" = 0 ] || { echo 'Run as root on the VPS' >&2; exit 1; }
}
value() {
    python3 - "$VAULT_ROOT/.env.vps" "$1" <<'PY'
import sys
from pathlib import Path
values = dict(line.split('=', 1) for line in Path(sys.argv[1]).read_text().splitlines()
              if line and not line.startswith('#') and '=' in line)
print(values[sys.argv[2]])
PY
}
lock_operations() {
    if [ "${VAULT_LOCK_HELD:-0}" != 1 ]; then
        exec 8>"$VAULT_ROOT/.vps.lock"
        flock -n 8 || { echo 'Another VPS operation is running' >&2; exit 1; }
        VAULT_LOCK_HELD=1
        export VAULT_LOCK_HELD
    fi
}
probe_ready() {
    compose exec -T gateway /usr/local/bin/healthcheck \
        -http "${VAULT_READY_URL:-https://$(value PUBLIC_IP)/ready}" -timeout 5s
}

# Legacy binaries ignore TLS environment variables; validate them before boot/update.
check_rpc_images() {
    [ -f "$VAULT_ROOT/.env.rpc" ] || return 0
    rpc_tag=${1:-$(value VAULT_IMAGE_TAG)}
    for rpc_service in auth chat media gateway; do
        docker run --rm --network none --entrypoint /usr/local/bin/healthcheck \
            -e RPC_TLS_MODE=mtls -e RPC_TLS_SERVICE="$rpc_service" -e RPC_TLS_DIR=/run/secrets/rpc \
            --mount "type=bind,src=$VAULT_ROOT/secrets/rpc/$rpc_service,dst=/run/secrets/rpc,readonly" \
            "vault-chat/$rpc_service:$rpc_tag" -check-rpc-tls
    done
}
