#!/bin/sh
# Validate private exposure and real Prometheus rules without starting a monitoring stack.
set -eu
cd "$(dirname -- "$0")/.."
for script in scripts/vps/*.sh; do sh -n "$script"; done
PUBLIC_IP=168.113.171.56 VAULT_IMAGE_TAG=validation \
AUTH_DB_PASSWORD=validation CHAT_DB_PASSWORD=validation MEDIA_DB_PASSWORD=validation \
AUTH_RUNTIME_DB_PASSWORD=validation CHAT_RUNTIME_DB_PASSWORD=validation MEDIA_RUNTIME_DB_PASSWORD=validation \
REDIS_PASSWORD=validation S3_ACCESS_KEY=validation S3_SECRET_KEY=validation \
S3_ROOT_ACCESS_KEY=root-validation S3_ROOT_SECRET_KEY=validation \
docker compose -f deployments/docker-compose.vps.yml -f deployments/monitoring/app.yml config --format json | python3 -c '
import json,sys
c=json.load(sys.stdin)
for service,spec in c["services"].items():
    for port in spec.get("ports",[]):
        if port["published"]=="443":assert service=="gateway"
        else:assert port["host_ip"]=="127.0.0.1",port
print("Application metrics ports are loopback-only")'
docker compose -f deployments/docker-compose.monitoring.yml config --format json | python3 -c '
import json,sys
c=json.load(sys.stdin)
for name,s in c["services"].items():
    assert "@sha256:" in s["image"]
    assert int(s["mem_limit"])>0
    if s.get("network_mode")=="host":
        if name=="grafana":assert s["environment"]["GF_SERVER_HTTP_ADDR"]=="127.0.0.1"
        else:assert any("127.0.0.1:" in a for a in s["command"]),name
    else:assert all(p["host_ip"]=="127.0.0.1" for p in s.get("ports",[]))
print("Monitoring UI/listeners are private; images and resources are pinned")'
docker run --rm --network none -v "$PWD/deployments/monitoring:/etc/prometheus:ro" \
    --entrypoint /bin/promtool prom/prometheus@sha256:fa3302279710be2f21a8301ec17a8b4f2301fd829bd113839b3b6b02b6f191a2 \
    test rules /etc/prometheus/alerts.test.yml
