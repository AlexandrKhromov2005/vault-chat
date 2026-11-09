#!/bin/sh
set -eu

archive=$1
version=$2
expected=$3
name=zig-x86_64-linux-$version.tar.xz
signature=$archive.minisig
public_key=RWSGOq2NVecA2UPNdBUZykf1CCb147pkmdtYxgb3Ti+JO/wCYvhbAb/U
mirrors=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/mirrors.txt

for mirror in $(cat "$mirrors") "https://ziglang.org/download/$version"; do
    url=$mirror/$name
    if ! wget -q -T 30 -t 1 -O "$archive" "$url?source=vault-chat" ||
        ! wget -q -T 30 -t 1 -O "$signature" "$url.minisig?source=vault-chat"; then
        continue
    fi
    if ! printf '%s  %s\n' "$expected" "$archive" | sha256sum -c - >/dev/null; then
        continue
    fi
    if ! comment=$(minisign -V -Q -P "$public_key" -x "$signature" -m "$archive"); then
        continue
    fi
    case $comment in
        *"file:$name"*)
            printf 'Verified %s from %s\n' "$name" "$mirror"
            exit 0
            ;;
    esac
done
rm -f "$archive" "$signature"
printf 'Unable to fetch and verify %s\n' "$name" >&2
exit 1
