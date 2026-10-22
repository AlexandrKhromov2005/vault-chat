#!/bin/sh
set -eu
. "$(dirname -- "$0")/common.sh"
root_only
case "${1:-status}" in
    install)
        lock_operations
        [ "$VAULT_ROOT" = /opt/vault-chat ] && [ "$VAULT_PROJECT" = vault-chat ] || {
            echo 'Systemd installation requires the standard VPS checkout/project' >&2; exit 1;
        }
        python3 "$VAULT_ROOT/scripts/monitoring/config.py" "$VAULT_ROOT"
        chown 472:472 "$VAULT_ROOT/secrets/grafana_admin_password"
        chmod 400 "$VAULT_ROOT/secrets/grafana_admin_password"
        chown -R 65534:65534 "$VAULT_ROOT/secrets/monitoring"
        chmod 700 "$VAULT_ROOT/secrets/monitoring"
        install -d -m 755 /usr/local/lib/vault-chat /var/lib/vault-chat/metrics
        install -m 644 "$VAULT_ROOT/scripts/monitoring/alerts.py" /usr/local/lib/vault-chat/alerts.py
        for file in "$VAULT_ROOT"/deployments/monitoring/*.service "$VAULT_ROOT"/deployments/monitoring/*.timer; do
            install -m 644 "$file" /etc/systemd/system/
        done
        monitor_compose run --rm --no-deps --entrypoint /bin/promtool prometheus check config /etc/prometheus/prometheus.yml
        monitor_compose run --rm --no-deps --entrypoint /bin/amtool alertmanager check-config /etc/alertmanager/alertmanager.yml
        compose up -d --no-build --wait
        systemctl daemon-reload
        python3 "$VAULT_ROOT/scripts/monitoring/operations.py"
        systemctl enable --now vault-alert-journal.service vault-operations.timer vault-monitoring.service
        ;;
    up)
        monitor_compose up -d --no-build
        python3 "$VAULT_ROOT/scripts/monitoring/config.py" "$VAULT_ROOT" ready
        ;;
    stop) monitor_compose stop;;
    status) monitor_compose ps;;
    *) echo 'Usage: scripts/vps.sh monitoring install|up|stop|status' >&2; exit 2;;
esac
