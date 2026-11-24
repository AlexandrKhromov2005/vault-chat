#!/bin/sh
set -eu
secret=$(cat /run/secrets/turn_secret)
config=/tmp/turnserver.conf
{
    printf '%s\n' 'listening-ip=0.0.0.0' 'listening-port=3478' 'fingerprint' 'use-auth-secret' 'realm=vault-chat' 'stale-nonce'
    printf '%s\n' 'max-allocate-lifetime=600' 'min-port=50000' 'max-port=50099' 'no-multicast-peers' 'pidfile=/tmp/turnserver.pid'
    printf 'static-auth-secret=%s\n' "$secret"
    printf 'external-ip=%s\n' "${TURN_PUBLIC_IP:?required}"
    if [ "${TURN_ALLOW_PRIVATE:-false}" != true ]; then
        printf '%s\n' 'denied-peer-ip=10.0.0.0-10.255.255.255' 'denied-peer-ip=172.16.0.0-172.31.255.255' 'denied-peer-ip=192.168.0.0-192.168.255.255' 'denied-peer-ip=169.254.0.0-169.254.255.255' 'denied-peer-ip=fc00::-fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff' 'denied-peer-ip=fe80::-febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff'
    else
        printf '%s\n' 'allow-loopback-peers'
    fi
} > "$config"
exec turnserver -c "$config"
