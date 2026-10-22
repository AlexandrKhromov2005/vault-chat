#!/bin/sh
# Application update. Database migrations are forward-only; no automatic rollback.
set -eu
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
[ $# = 1 ] || { echo 'Usage: scripts/vps.sh update IMAGE_TAG' >&2; exit 1; }
tag=$1
case "$tag" in ''|*[!a-zA-Z0-9_.-]*) echo 'Invalid image tag' >&2; exit 1;; esac
for service in auth chat media gateway minio; do
    docker image inspect "vault-chat/$service:$tag" >/dev/null
done
"$VAULT_ROOT/scripts/vps.sh" backup
# Change only the image selector; never regenerate passwords or JWT keys.
python3 - "$VAULT_ROOT/.env.vps" "$tag" <<'PY'
from pathlib import Path
import os, sys
p=Path(sys.argv[1]); lines=p.read_text().splitlines()
assert sum(line.startswith('VAULT_IMAGE_TAG=') for line in lines) == 1
lines=[f'VAULT_IMAGE_TAG={sys.argv[2]}' if line.startswith('VAULT_IMAGE_TAG=') else line for line in lines]
fd=os.open(str(p)+'.next', os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
with os.fdopen(fd,'w') as f: f.write('\n'.join(lines)+'\n')
os.replace(str(p)+'.next',p)
PY
compose up -d --no-build --wait
# Probe dependencies as well as the listener. Health probe verifies the IP cert.
probe_ready
echo 'Update complete; the previous state is retained in the backup'
