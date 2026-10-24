#!/bin/sh
set -eu
umask 077
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
command -v age >/dev/null
command -v rclone >/dev/null
recipient=$(value BACKUP_AGE_RECIPIENT 2>/dev/null) || { echo 'Set BACKUP_AGE_RECIPIENT in .env.vps' >&2; exit 1; }
remote=$(value BACKUP_REMOTE 2>/dev/null) || { echo 'Set BACKUP_REMOTE in .env.vps' >&2; exit 1; }
rclone_config=$(value BACKUP_RCLONE_CONFIG 2>/dev/null) || rclone_config=/etc/vault-chat/rclone.conf
python3 "$VAULT_ROOT/scripts/vps/backup_config.py" "$remote" "$rclone_config"
printf '' | age --recipient "$recipient" >/dev/null
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
    python3 "$VAULT_ROOT/scripts/vps/encrypted_backup.py" "$recipient" "$backup_dir/$user.dump.age" \
        docker compose -p "$VAULT_PROJECT" --env-file "$VAULT_ROOT/.env.vps" -f "$VAULT_ROOT/deployments/docker-compose.vps.yml" \
        exec -T "$service" pg_dump -U "$user" -Fc "${user}_db"
done
media_path=$(docker volume inspect "${VAULT_PROJECT}_miniodata" --format '{{.Mountpoint}}')
python3 "$VAULT_ROOT/scripts/vps/encrypted_backup.py" "$recipient" "$backup_dir/media-objects.tar.gz.age" tar -C "$media_path" -czf - .
set -- .env.vps secrets deployments/docker-compose.vps.yml
if [ -f "$VAULT_ROOT/.env.monitoring" ]; then set -- "$@" .env.monitoring deployments/monitoring/app.yml; fi
if [ -f "$VAULT_ROOT/.env.rpc" ]; then set -- "$@" .env.rpc deployments/docker-compose.mtls.yml; fi
python3 "$VAULT_ROOT/scripts/vps/encrypted_backup.py" "$recipient" "$backup_dir/config.tar.gz.age" tar -C "$VAULT_ROOT" -czf - "$@"
compose config --images > "$backup_dir/images.txt"
cleanup
paused_ids=''
(cd "$backup_dir" && sha256sum auth.dump.age chat.dump.age media.dump.age config.tar.gz.age media-objects.tar.gz.age images.txt > SHA256SUMS)
destination="${remote%/}/${backup_dir##*/}"
rclone --config "$rclone_config" copy --immutable --checksum "$backup_dir" "$destination"
rclone --config "$rclone_config" check --download --one-way "$backup_dir" "$destination"
printf '2\n' > "$backup_dir/.COMPLETE.next"
rclone --config "$rclone_config" copyto --immutable "$backup_dir/.COMPLETE.next" "$destination/COMPLETE"
mv "$backup_dir/.COMPLETE.next" "$backup_dir/COMPLETE"
# Retain successful copies only; failed copies need operator inspection.
find "$backup_root" -mindepth 2 -maxdepth 2 -type f -name COMPLETE -mtime +6 -exec sh -c 'rm -rf -- "$(dirname -- "$1")"' sh {} \;
echo "Encrypted backup verified off-host: $backup_dir"
