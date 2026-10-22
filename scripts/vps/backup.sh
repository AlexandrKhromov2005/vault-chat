#!/bin/sh
set -eu
umask 077
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
backup_root=${VAULT_BACKUP_ROOT:-/var/backups/vault-chat}
mkdir -p "$backup_root"
chmod 700 "$backup_root"
backup_dir="$backup_root/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir "$backup_dir"
paused_ids=''
cleanup() {
    if [ -n "$paused_ids" ]; then docker unpause $paused_ids >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
# Capture IDs before pausing so even a partial pause failure is undone.
paused_ids=$(compose ps --status running -q auth chat media gateway minio)
if [ -n "$paused_ids" ]; then docker pause $paused_ids >/dev/null; fi
for user in auth chat media; do
    case "$user" in auth) service=postgres;; *) service="$user-postgres";; esac
    compose exec -T "$service" pg_dump -U "$user" -Fc "${user}_db" > "$backup_dir/$user.dump"
done
media_path=$(docker volume inspect "${VAULT_PROJECT}_miniodata" --format '{{.Mountpoint}}')
tar -C "$media_path" -czf "$backup_dir/media-objects.tar.gz" .
set -- .env.vps secrets deployments/docker-compose.vps.yml
if [ -f "$VAULT_ROOT/.env.monitoring" ]; then set -- "$@" .env.monitoring deployments/monitoring/app.yml; fi
tar -C "$VAULT_ROOT" -czf "$backup_dir/config.tar.gz" "$@"
compose config --images > "$backup_dir/images.txt"
cleanup
paused_ids=''
for user in auth chat media; do
    compose exec -T postgres pg_restore --list < "$backup_dir/$user.dump" >/dev/null
done
tar -tzf "$backup_dir/media-objects.tar.gz" >/dev/null
(cd "$backup_dir" && sha256sum auth.dump chat.dump media.dump config.tar.gz media-objects.tar.gz images.txt > SHA256SUMS)
printf '1\n' > "$backup_dir/COMPLETE"
# Retain successful copies only; failed copies need operator inspection.
find "$backup_root" -mindepth 2 -maxdepth 2 -type f -name COMPLETE -mtime +6 -exec sh -c 'rm -rf -- "$(dirname -- "$1")"' sh {} \;
echo "Backup completed: $backup_dir"
