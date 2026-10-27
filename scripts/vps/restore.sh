#!/bin/sh
# Restore into a fresh checkout/project. Never overwrite existing volumes.
set -eu
umask 077
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
[ $# = 1 ] || { echo 'Usage: scripts/vps.sh restore BACKUP_DIR' >&2; exit 1; }
backup_dir=$(realpath "$1")
[ ! -e "$VAULT_ROOT/.env.vps" ] && [ ! -e "$VAULT_ROOT/.env.email" ] && [ ! -e "$VAULT_ROOT/secrets" ] || {
    echo 'Restore requires a fresh checkout without .env.vps, .env.email or secrets' >&2; exit 1;
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
restore_stage=''
cleanup_restore() { if [ -n "$restore_stage" ]; then rm -rf -- "$restore_stage"; fi; }
trap cleanup_restore EXIT
trap 'exit 1' HUP INT TERM
if [ "$(cat "$backup_dir/COMPLETE")" = 2 ]; then
    [ -n "${VAULT_BACKUP_IDENTITY:-}" ] || { echo 'Set VAULT_BACKUP_IDENTITY to the off-host age identity file' >&2; exit 1; }
    restore_stage=$(mktemp -d)
    python3 "$VAULT_ROOT/scripts/vps/archive.py" "$backup_dir" "$restore_stage" "$VAULT_BACKUP_IDENTITY"
    backup_dir=$restore_stage
fi
# No image pulls or builds while restoring: load the recorded images first.
while IFS= read -r image; do docker image inspect "$image" >/dev/null; done < "$backup_dir/images.txt"
# Inspect only bounded configuration metadata before extracting live files.
# A pre-email Auth image must not ignore stored ownership policy, even when
# delivery was paused and only the historical outbox key remains.
mail_restore_tag=$(python3 - "$backup_dir/config.tar.gz" <<'PY'
import re, sys, tarfile
from pathlib import PurePosixPath
with tarfile.open(sys.argv[1]) as archive:
    members = {PurePosixPath(member.name).as_posix(): member for member in archive.getmembers()}
    if '.env.email' not in members and 'secrets/mail/outbox.key' not in members:
        sys.exit(0)
    with archive.extractfile(members['.env.vps']) as stream:
        data = stream.read(65537)
    if len(data) > 65536:
        raise ValueError('Restored environment exceeds the configuration budget')
    tags = [line.split('=', 1)[1] for line in data.decode().splitlines() if line.startswith('VAULT_IMAGE_TAG=')]
    if len(tags) != 1 or not re.fullmatch(r'[A-Za-z0-9_.-]+', tags[0]):
        raise ValueError('A single restored image selector is required')
    print(tags[0])
PY
)
if [ -n "$mail_restore_tag" ]; then check_email_image "$mail_restore_tag"; fi
tar --no-same-owner -C "$VAULT_ROOT" -xzf "$backup_dir/config.tar.gz"
chmod 600 "$VAULT_ROOT/.env.vps"
chown 65532:65532 "$VAULT_ROOT/secrets/auth_jwt_private.pem" "$VAULT_ROOT/secrets/auth_jwt_public.pem"
chmod 400 "$VAULT_ROOT/secrets/auth_jwt_private.pem"
chmod 444 "$VAULT_ROOT/secrets/auth_jwt_public.pem"
chown -R 65532:65532 "$VAULT_ROOT/secrets/tls"
if [ -f "$VAULT_ROOT/.env.email" ]; then
    chmod 600 "$VAULT_ROOT/.env.email"
fi
if email_policy_present; then
    chown -R 65532:65532 "$VAULT_ROOT/secrets/mail"
    find "$VAULT_ROOT/secrets/mail" -type d -exec chmod 700 {} +
    find "$VAULT_ROOT/secrets/mail" -type f -exec chmod 400 {} +
fi
if [ -f "$VAULT_ROOT/.env.rpc" ]; then
    chown -R 65532:65532 "$VAULT_ROOT/secrets/rpc"
    chmod 700 "$VAULT_ROOT/secrets/rpc/"*
    chmod 400 "$VAULT_ROOT/secrets/rpc/"*/tls.key
    chmod 700 "$VAULT_ROOT/secrets/rpc-ca"
    chmod 600 "$VAULT_ROOT/secrets/rpc-ca/ca.key"
fi
check_rpc_images
compose up -d --no-build --pull never --wait postgres chat-postgres media-postgres redis
for user in auth chat media; do
    case "$user" in auth) service=postgres;; *) service="$user-postgres";; esac
    compose exec -T "$service" pg_restore -U "$user" --no-owner --no-acl --exit-on-error \
        -d "${user}_db" < "$backup_dir/$user.dump"
done
# Create the S3 volume without starting MinIO against an empty directory.
compose create --no-build --pull never minio
media_path=$(docker volume inspect "${VAULT_PROJECT}_miniodata" --format '{{.Mountpoint}}')
tar --no-same-owner -C "$media_path" -xzf "$backup_dir/media-objects.tar.gz"
chown -R 65532:65532 "$media_path"
compose up -d --no-build --pull never --wait
probe_ready
echo 'Restore complete; configure HTTPS renewal on the target host with install'
