#!/bin/sh
# Single-host VPS commands. See docs/deployment.md.
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/vps/common.sh"
command=${1:-help}
[ $# = 0 ] || shift
case "$command" in
    init) root_only; exec python3 "$script_dir/vps/init.py" "$@";;
    build)
        [ $# = 1 ] || { echo 'Usage: scripts/vps.sh build IMAGE_TAG' >&2; exit 1; }
        tag=$1
        case "$tag" in ''|*[!a-zA-Z0-9_.-]*) echo 'Invalid image tag' >&2; exit 1;; esac
        for service in auth chat media gateway minio; do
            image="vault-chat/$service:$tag"
            if docker image inspect "$image" >/dev/null 2>&1; then
                echo "Refusing to overwrite existing image $image" >&2; exit 1
            fi
        done
        revision=$(git -C "$script_dir/.." rev-parse HEAD 2>/dev/null || cat "$script_dir/../DEPLOYED_COMMIT")
        for service in auth chat media gateway; do
            docker build --network "${VAULT_BUILD_NETWORK:-default}" --build-arg "SERVICE=$service" \
                --label "org.opencontainers.image.revision=${revision}" \
                -t "vault-chat/$service:$tag" "$script_dir/.."
        done
        docker build --network "${VAULT_BUILD_NETWORK:-default}" -f "$script_dir/../Dockerfile.minio" \
            -t "vault-chat/minio:$tag" "$script_dir/..";;
    up) root_only; check_rpc_images; compose up -d --no-build --wait;;
    status) compose ps;;
    install|update|backup|restore|tls|monitoring|mtls)
        root_only; exec "$script_dir/vps/$command.sh" "$@";;
    *) echo 'Usage: scripts/vps.sh {init IP TAG|build TAG|install|up|status|update TAG|backup|restore BACKUP_DIR|tls|monitoring install/up/stop/status|mtls init/enable/rotate/status}' >&2; exit 2;;
esac
