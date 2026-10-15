#!/bin/sh
# Generate the development RS256 key pair used by the Auth service.
#
# Usage: scripts/gen-jwt-keys.sh [DIR]   (default DIR: secrets)
#
# An existing private key is kept; delete it to rotate. The public key is
# (re)derived whenever it is missing, does not match the private key, or a
# new private key is generated. Rerunning the script on a matching pair only
# reapplies the permissions below.
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

derive_public_key() {
	openssl rsa -in "$private_key" -pubout -out "$public_key"
	echo "generated $public_key from $private_key"
}

# The public key is always derived from the private key, never kept on its
# own: a public key left over from an earlier pair would make Auth sign
# tokens it cannot verify.
if [ -f "$private_key" ]; then
	echo "keeping existing $private_key"
	if [ ! -f "$public_key" ]; then
		derive_public_key
	elif ! openssl rsa -in "$private_key" -pubout 2>/dev/null | cmp -s - "$public_key"; then
		echo "$public_key does not match $private_key, replacing it"
		derive_public_key
	else
		echo "keeping existing $public_key"
	fi
else
	openssl genrsa -out "$private_key" 4096
	echo "generated $private_key"
	derive_public_key
fi

chmod 644 "$private_key" "$public_key"
