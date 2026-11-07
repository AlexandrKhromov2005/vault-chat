#!/bin/sh
set -eu
cd "$(dirname -- "$0")/../.."
case "$(docker context inspect --format '{{.Endpoints.docker.Host}}')" in
    unix://*) ;;
    *) echo 'Redis acceptance requires a local Unix Docker endpoint' >&2; exit 1;;
esac
project="vault-redis-acceptance-$$"
export PUBLIC_IP=168.113.171.56 VAULT_IMAGE_TAG=validation
export AUTH_DB_PASSWORD=validation CHAT_DB_PASSWORD=validation MEDIA_DB_PASSWORD=validation
export AUTH_RUNTIME_DB_PASSWORD=validation CHAT_RUNTIME_DB_PASSWORD=validation MEDIA_RUNTIME_DB_PASSWORD=validation
export REDIS_PASSWORD=acceptance-redis-password PROFILE_CACHE_PASSWORD=acceptance-cache-password
export S3_ACCESS_KEY=validation S3_SECRET_KEY=validation
export S3_ROOT_ACCESS_KEY=root-validation S3_ROOT_SECRET_KEY=validation
cleanup() {
    docker rm -f "$project-seed" >/dev/null 2>&1 || true
    docker compose -p "$project" -f deployments/docker-compose.vps.yml down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker volume create "${project}_redisdata" >/dev/null
docker run -d --name "$project-seed" --network none --memory 64m \
    -e REDIS_PASSWORD -v "${project}_redisdata:/data" \
    redis:7-alpine@sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499 \
    sh -c 'exec redis-server --requirepass "$REDIS_PASSWORD" --appendonly no' >/dev/null
for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if docker exec "$project-seed" redis-cli --no-auth-warning -a "$REDIS_PASSWORD" ping 2>/dev/null | grep -q PONG; then break; fi
    sleep 1
done
docker exec "$project-seed" redis-cli --no-auth-warning -a "$REDIS_PASSWORD" set acceptance seeded | grep -q OK
docker exec "$project-seed" redis-cli --no-auth-warning -a "$REDIS_PASSWORD" save | grep -q OK
docker stop -t 5 "$project-seed" >/dev/null
docker rm "$project-seed" >/dev/null
compose() {
    docker compose -p "$project" -f deployments/docker-compose.vps.yml "$@"
}
compose up -d --no-build --wait redis profile-cache
[ "$(compose exec -T redis id -u)" = 999 ]
[ "$(compose exec -T profile-cache id -u)" = 999 ]
[ "$(compose exec -T redis redis-cli --no-auth-warning -a "$REDIS_PASSWORD" get acceptance)" = seeded ]
compose exec -T redis redis-cli --no-auth-warning -a "$REDIS_PASSWORD" set acceptance hardened | grep -q OK
compose exec -T redis redis-cli --no-auth-warning -a "$REDIS_PASSWORD" save | grep -q OK
compose exec -T redis sh -c 'ls -ln /data/dump.rdb | awk "\$3 == 999 && \$4 == 1000 {ok=1} END {exit !ok}"'
compose restart redis >/dev/null
compose up -d --no-build --wait redis
[ "$(compose exec -T redis redis-cli --no-auth-warning -a "$REDIS_PASSWORD" get acceptance)" = hardened ]
compose exec -T profile-cache redis-cli --no-auth-warning -a "$PROFILE_CACHE_PASSWORD" ping | grep -q PONG
printf '%s\n' 'Redis acceptance passed: legacy RDB, non-root runtime, save and restart'
