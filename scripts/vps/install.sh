#!/bin/sh
# Run on a dedicated Ubuntu 24.04 VPS after init and image delivery.
set -eu
. "$(dirname -- "$0")/common.sh"
root_only
lock_operations
[ "$VAULT_ROOT" = /opt/vault-chat ] || { echo 'Install the checkout in /opt/vault-chat' >&2; exit 1; }
. /etc/os-release
[ "$ID:$VERSION_ID" = ubuntu:24.04 ] || { echo 'Installer requires Ubuntu 24.04' >&2; exit 1; }
# Validate identity and image availability before touching host configuration.
ip=$(value PUBLIC_IP)
python3 -c 'import ipaddress,sys; a=ipaddress.ip_address(sys.argv[1]); assert a.version == 4 and a.is_global' "$ip"
compose config --quiet
for service in auth chat media gateway minio; do
    docker image inspect "vault-chat/$service:$(value VAULT_IMAGE_TAG)" >/dev/null
 done
for site in /etc/nginx/sites-enabled/*; do
    case "${site##*/}" in default|vault-chat|'*') ;; *) echo "Existing nginx site: $site; configure manually" >&2; exit 1;; esac
 done
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y docker.io docker-compose-v2 nginx python3-venv ufw age rclone
systemctl enable --now docker nginx
systemctl enable ssh
mkdir -p /var/www/letsencrypt/.well-known/acme-challenge
sed "s/@PUBLIC_IP@/$ip/g" "$VAULT_ROOT/deployments/vps/nginx.conf" > /etc/nginx/sites-available/vault-chat
rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/vault-chat /etc/nginx/sites-enabled/vault-chat
nginx -t
systemctl reload nginx
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
[ -x /opt/certbot/bin/certbot ] || python3 -m venv /opt/certbot
/opt/certbot/bin/pip install 'certbot==5.8.0'
/opt/certbot/bin/certbot certonly --webroot -w /var/www/letsencrypt \
    --ip-address "$ip" --preferred-profile shortlived --non-interactive \
    --agree-tos --register-unsafely-without-email
"$VAULT_ROOT/scripts/vps.sh" tls
install -d /etc/letsencrypt/renewal-hooks/deploy
printf '#!/bin/sh\nexec /opt/vault-chat/scripts/vps.sh tls\n' > /etc/letsencrypt/renewal-hooks/deploy/vault-chat
chmod 700 /etc/letsencrypt/renewal-hooks/deploy/vault-chat
for file in "$VAULT_ROOT"/deployments/vps/*.service "$VAULT_ROOT"/deployments/vps/*.timer; do
    install -m 644 "$file" /etc/systemd/system/
done
systemctl daemon-reload
systemctl enable --now vault-certbot.timer vault-chat-backup.timer vault-chat.service
