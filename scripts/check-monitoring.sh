#!/bin/sh
# Validate private addressing, exposure and real Prometheus rules.
set -eu
cd "$(dirname -- "$0")/.."
for script in scripts/vps/*.sh; do sh -n "$script"; done
PUBLIC_IP=168.113.171.56 VAULT_IMAGE_TAG=validation \
AUTH_DB_PASSWORD=validation CHAT_DB_PASSWORD=validation MEDIA_DB_PASSWORD=validation \
AUTH_RUNTIME_DB_PASSWORD=validation CHAT_RUNTIME_DB_PASSWORD=validation MEDIA_RUNTIME_DB_PASSWORD=validation \
REDIS_PASSWORD=validation PROFILE_CACHE_PASSWORD=cache-validation S3_ACCESS_KEY=validation S3_SECRET_KEY=validation \
S3_ROOT_ACCESS_KEY=root-validation S3_ROOT_SECRET_KEY=validation \
docker compose -f deployments/docker-compose.vps.yml -f deployments/monitoring/app.yml config --format json | python3 -c '
import ipaddress,json,sys
c=json.load(sys.stdin)
expected={"auth":("172.31.240.2","metrics-auth"),"chat":("172.31.240.10","metrics-chat"),
          "media":("172.31.240.18","metrics-media"),"gateway":("172.31.240.26","metrics-gateway")}
subnets=[]
for service,(address,network) in expected.items():
    spec=c["services"][service]
    assert spec["environment"][service.upper()+"_METRICS_ADDR"]==address+":9100"
    assert spec["networks"][network]["ipv4_address"]==address
    assert c["networks"][network]["internal"] is True
    subnet=c["networks"][network]["ipam"]["config"][0]["subnet"]
    subnets.append(subnet)
    assert ipaddress.ip_address(address) in ipaddress.ip_network(subnet)
    assert sum(network in s.get("networks",{}) for s in c["services"].values())==1
assert len(set(subnets))==len(subnets)
for service,spec in c["services"].items():
    for port in spec.get("ports",[]):
        assert service=="gateway" and port["published"]=="443",port
for service,address,network in [("postgres","172.31.240.34","auth-data"),
    ("chat-postgres","172.31.240.42","chat-data"),("media-postgres","172.31.240.50","media-data"),
    ("redis","172.31.240.58","events"),("minio","172.31.240.66","objects")]:
    assert c["services"][service]["networks"][network]["ipv4_address"]==address
    assert c["networks"][network]["internal"] is True
print("Application metrics and probes use isolated fixed private addresses")'
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
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf 'PUBLIC_IP=168.113.171.56\n' > "$tmp/.env.vps"
mkdir -p "$tmp/deployments/monitoring"
PYTHONDONTWRITEBYTECODE=1 python3 scripts/monitoring/config.py "$tmp"
cp deployments/monitoring/alerts.test.yml "$tmp/deployments/monitoring/alerts.test.yml"
docker run --rm --network none -v "$tmp/deployments/monitoring:/etc/prometheus:ro" \
    --entrypoint /bin/promtool prom/prometheus@sha256:fa3302279710be2f21a8301ec17a8b4f2301fd829bd113839b3b6b02b6f191a2 \
    test rules /etc/prometheus/alerts.test.yml
