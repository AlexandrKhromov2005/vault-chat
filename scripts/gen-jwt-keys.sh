#!/bin/sh
# Generate the development RS256 key pair used by the Auth service.
#
# Usage: scripts/gen-jwt-keys.sh [DIR]   (default DIR: secrets)
#
# Existing keys are kept; delete them to rotate. Rerunning the script on
# existing keys only reapplies the permissions below.
#
# Permissions: the containers run as a non-root user (uid 65532) that differs
# from the host user, and Compose bind-mounts the key files as they are. The
# key files are therefore world-readable, while the directory is accessible to
# the owner only, so other host users still cannot reach them. These are
# development keys; production keys belong in a secret store.
set -eu

dir=${1:-secrets}
private_key="$dir/auth_jwt_private.pem"
public_key="$dir/auth_jwt_public.pem"

command -v openssl >/dev/null 2>&1 || {
	echo "openssl is required" >&2
	exit 1
}

mkdir -p "$dir"
chmod 700 "$dir"

# Docker Desktop creates an empty directory in place of a missing bind-mount
# source when the stack is started before the keys exist. Remove those so
# the keys can be generated; rmdir refuses to delete anything non-empty.
for key in "$private_key" "$public_key"; do
	if [ -d "$key" ]; then
		rmdir "$key"
		echo "removed empty directory $key left by Docker"
	fi
done

if [ -f "$private_key" ]; then
	echo "keeping existing $private_key"
else
	openssl genrsa -out "$private_key" 4096
	echo "generated $private_key"
fi

if [ -f "$public_key" ]; then
	echo "keeping existing $public_key"
else
	openssl rsa -in "$private_key" -pubout -out "$public_key"
	echo "generated $public_key"
fi

chmod 644 "$private_key" "$public_key"
