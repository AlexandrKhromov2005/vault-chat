#!/bin/sh
set -eu
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
ip=$(value PUBLIC_IP)
lineage="/etc/letsencrypt/live/$ip"
[ -z "${RENEWED_LINEAGE:-}" ] || [ "$RENEWED_LINEAGE" = "$lineage" ] || exit 0
install -d -o 65532 -g 65532 -m 700 "$VAULT_ROOT/secrets/tls"
for file in fullchain.pem privkey.pem; do
    install -o 65532 -g 65532 -m 600 "$lineage/$file" "$VAULT_ROOT/secrets/tls/$file"
done
# A fresh installation has no gateway yet. Only restart an existing one.
if [ -n "$(compose ps -q gateway)" ]; then compose restart gateway; fi
