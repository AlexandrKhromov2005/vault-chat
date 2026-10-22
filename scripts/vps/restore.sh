#!/bin/sh
# Restore into a fresh checkout/project. Never overwrite existing volumes.
set -eu
umask 077
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
[ $# = 1 ] || { echo 'Usage: scripts/vps.sh restore BACKUP_DIR' >&2; exit 1; }
backup_dir=$(realpath "$1")
[ ! -e "$VAULT_ROOT/.env.vps" ] && [ ! -e "$VAULT_ROOT/secrets" ] || {
    echo 'Restore requires a fresh checkout without .env.vps or secrets' >&2; exit 1;
}
[ -z "$(docker volume ls -q --filter "label=com.docker.compose.project=$VAULT_PROJECT")" ] || {
    echo 'Project volumes already exist; restore into a fresh project' >&2; exit 1;
}
for volume in pgdata chat_pgdata media_pgdata redisdata miniodata; do
    if docker volume inspect "${VAULT_PROJECT}_$volume" >/dev/null 2>&1; then
        echo "Volume ${VAULT_PROJECT}_$volume already exists; refusing restore" >&2; exit 1
    fi
done
python3 "$VAULT_ROOT/scripts/vps/archive.py" "$backup_dir"
# No image pulls or builds while restoring: load the recorded images first.
while IFS= read -r image; do docker image inspect "$image" >/dev/null; done < "$backup_dir/images.txt"
tar --no-same-owner -C "$VAULT_ROOT" -xzf "$backup_dir/config.tar.gz"
chmod 600 "$VAULT_ROOT/.env.vps"
chown -R 65532:65532 "$VAULT_ROOT/secrets/tls"
compose up -d --no-build --pull never --wait postgres chat-postgres media-postgres redis
for user in auth chat media; do
    case "$user" in auth) service=postgres;; *) service="$user-postgres";; esac
    compose exec -T "$service" pg_restore -U "$user" --no-owner --exit-on-error \
        -d "${user}_db" < "$backup_dir/$user.dump"
done
# Create the S3 volume without starting MinIO against an empty directory.
compose create --no-build --pull never minio
media_path=$(docker volume inspect "${VAULT_PROJECT}_miniodata" --format '{{.Mountpoint}}')
tar --no-same-owner -C "$media_path" -xzf "$backup_dir/media-objects.tar.gz"
chown -R 65532:65532 "$media_path"
compose up -d --no-build --pull never --wait
compose exec -T gateway /usr/local/bin/healthcheck -http "https://$(value PUBLIC_IP)/ready" -timeout 5s
echo 'Restore complete; configure HTTPS renewal on the target host with install'
