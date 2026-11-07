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
REDIS_PASSWORD=validation PROFILE_CACHE_PASSWORD=cache-validation S3_ACCESS_KEY=validation S3_SECRET_KEY=validation \
S3_ROOT_ACCESS_KEY=root-validation S3_ROOT_SECRET_KEY=validation \
docker compose -f deployments/docker-compose.vps.yml config --format json | python3 -c '
import json,sys
c=json.load(sys.stdin); services=c["services"]
assert len(services)==15
for name in ["redis","profile-cache"]:
    service=services[name]
    assert service["user"]=="redis"
    assert service["read_only"] is True
    assert service["cap_drop"]==["ALL"]
    assert "no-new-privileges:true" in service["security_opt"]
    assert service["depends_on"]["redis-permissions"]["condition"]=="service_completed_successfully"
permissions=services["redis-permissions"]
assert permissions["network_mode"]=="none"
assert permissions["read_only"] is True
assert permissions["cap_drop"]==["ALL"]
assert sorted(permissions["cap_add"])==["CHOWN","DAC_OVERRIDE","FOWNER"]
assert "no-new-privileges:true" in permissions["security_opt"]
assert permissions["restart"]=="no" and int(permissions["mem_limit"])<=33554432
# The profile cache is its own Redis on an internal network only the gateway
# joins, bounded and never persisted, so its evictions cannot reach rate limits.
cache=services["profile-cache"]
assert set(cache["networks"])=={"profile-cache"} and c["networks"]["profile-cache"]["internal"]
assert sorted(n for n,s in services.items() if "profile-cache" in s.get("networks",{}))==["gateway","profile-cache"]
assert all(a in cache["command"][-1] for a in ["--maxmemory 16mb","allkeys-lru","--save \"\"","--appendonly no"])
gw=services["gateway"]["environment"]
assert gw["GATEWAY_PROFILE_CACHE_ADDR"]=="profile-cache:6379"!=gw["GATEWAY_REDIS_ADDR"]
assert gw["GATEWAY_PROFILE_CACHE_PASSWORD"]!=gw["GATEWAY_REDIS_PASSWORD"]
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
    assert s["restart"]==("no" if name.endswith("-migrate") or name in {"s3-init","redis-permissions"} else "unless-stopped")
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
REDIS_PASSWORD=validation PROFILE_CACHE_PASSWORD=cache-validation S3_ACCESS_KEY=validation S3_SECRET_KEY=validation \
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
AUTH_DB_PASSWORD=validation CHAT_DB_PASSWORD=validation MEDIA_DB_PASSWORD=validation \
AUTH_RUNTIME_DB_PASSWORD=validation CHAT_RUNTIME_DB_PASSWORD=validation MEDIA_RUNTIME_DB_PASSWORD=validation \
REDIS_PASSWORD=validation S3_ACCESS_KEY=validation S3_SECRET_KEY=validation \
S3_ROOT_ACCESS_KEY=root-validation S3_ROOT_SECRET_KEY=validation \
VAULT_IMAGE_TAG=validation PUBLIC_IP=198.51.100.10 docker compose -f deployments/docker-compose.vps.yml -f deployments/docker-compose.push.yml config --format json | python3 -c '
import json,sys
c=json.load(sys.stdin)
auth=c["services"]["auth"]
assert auth["environment"]["AUTH_PUSH_FCM_CREDENTIALS_FILE"]=="/run/secrets/push/fcm.json"
v=[v for v in auth["volumes"] if v["target"]=="/run/secrets/push"]
assert len(v)==1 and v[0]["source"].endswith("/secrets/push")
assert v[0]["read_only"] and not v[0]["bind"].get("create_host_path",False)
assert "push-egress" in auth["networks"] and not c["networks"]["push-egress"].get("internal",False)
for name,s in c["services"].items():
    assert name=="auth" or ("push-egress" not in s.get("networks",{}) and all("/secrets/push" not in v["source"] for v in s.get("volumes",[]) if isinstance(v,dict)))
assert not auth.get("ports")
print("Push overlay: key and egress for Auth only, read-only mount, no new ports")'
