#!/bin/sh
# Local deployment checks; no credentials, root access or running stack needed.
set -eu
cd "$(dirname -- "$0")/.."
export PYTHONDONTWRITEBYTECODE=1
for script in scripts/vps.sh scripts/vps/*.sh; do sh -n "$script"; done
python3 -m unittest discover -s tests/ops -v
PUBLIC_IP=168.113.171.56 VAULT_IMAGE_TAG=validation \
AUTH_DB_PASSWORD=validation CHAT_DB_PASSWORD=validation MEDIA_DB_PASSWORD=validation \
AUTH_RUNTIME_DB_PASSWORD=validation CHAT_RUNTIME_DB_PASSWORD=validation MEDIA_RUNTIME_DB_PASSWORD=validation \
REDIS_PASSWORD=validation S3_ACCESS_KEY=validation S3_SECRET_KEY=validation \
S3_ROOT_ACCESS_KEY=root-validation S3_ROOT_SECRET_KEY=validation \
docker compose -f deployments/docker-compose.vps.yml config --format json | python3 -c '
import json,sys
c=json.load(sys.stdin); services=c["services"]
assert len(services)==13
for name,db in [("auth","postgres"),("chat","chat-postgres"),("media","media-postgres")]:
    assert name+"_app:" in services[name]["environment"][name.upper()+"_DATABASE_URL"]
    assert services[name]["depends_on"][name+"-migrate"]["condition"]=="service_completed_successfully"
    own=set(services[db]["networks"])
    for other in ["auth","chat","media","gateway"]:
        if other!=name:
            assert not own.intersection(services[other]["networks"])
assert services["minio"]["environment"]["MINIO_ROOT_USER"] != services["media"]["environment"]["MEDIA_S3_ACCESS_KEY"]
assert set(services["minio"]["networks"])=={"objects"}
for name,s in services.items():
    assert "build" not in s
    assert s["restart"]==("no" if name.endswith("-migrate") or name=="s3-init" else "unless-stopped")
    assert int(s["mem_limit"])>0
    assert s["logging"]["options"]["max-size"]=="10m"
    ports=s.get("ports",[])
    if name=="gateway":
        assert len(ports)==1 and ports[0]["published"]=="443"
        assert s["environment"]["GATEWAY_TLS_KEY_PATH"]
    else:
        assert not ports, name
print("VPS Compose: TLS, private data services, limits and image-only deployment verified")'

PUBLIC_IP=168.113.171.56 VAULT_IMAGE_TAG=validation \
AUTH_DB_PASSWORD=validation CHAT_DB_PASSWORD=validation MEDIA_DB_PASSWORD=validation \
AUTH_RUNTIME_DB_PASSWORD=validation CHAT_RUNTIME_DB_PASSWORD=validation MEDIA_RUNTIME_DB_PASSWORD=validation \
REDIS_PASSWORD=validation S3_ACCESS_KEY=validation S3_SECRET_KEY=validation \
S3_ROOT_ACCESS_KEY=root-validation S3_ROOT_SECRET_KEY=validation \
docker compose -f deployments/docker-compose.vps.yml -f deployments/docker-compose.mtls.yml config --format json | python3 -c '
import json,sys
c=json.load(sys.stdin)
for name in ["auth","chat","media","gateway"]:
    s=c["services"][name]
    assert s["environment"]["RPC_TLS_MODE"]=="mtls"
    assert s["environment"]["RPC_TLS_SERVICE"]==name
    v=[v for v in s["volumes"] if v["target"]=="/run/secrets/rpc"]
    assert len(v)==1 and v[0]["source"].endswith("/secrets/rpc/"+name)
    assert v[0]["read_only"] and not v[0]["bind"].get("create_host_path",False)
    assert all("rpc-ca" not in v["source"] for v in s["volumes"])
print("mTLS overlay: isolated service keys, no CA signing key mounts")'
