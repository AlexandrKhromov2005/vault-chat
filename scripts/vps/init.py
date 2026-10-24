"""Initialize a fresh VPS without replacing existing credentials or keys."""
import ipaddress
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys

if len(sys.argv) != 3:
    sys.exit('Usage: scripts/vps.sh init PUBLIC_IPV4 IMAGE_TAG')
ip, tag = sys.argv[1:]
address = ipaddress.ip_address(ip)
if address.version != 4 or not address.is_global:
    sys.exit('A public IPv4 address is required')
if not re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}', tag):
    sys.exit('Invalid image tag')
root = Path(os.environ['VAULT_ROOT'])
root.mkdir(parents=True, exist_ok=True)
env = root / '.env.vps'
if env.exists():
    values = dict(line.split('=', 1) for line in env.read_text().splitlines()
                  if line and not line.startswith('#') and '=' in line)
    for key, expected in [('PUBLIC_IP', ip), ('VAULT_IMAGE_TAG', tag)]:
        if key in values and values[key] != expected:
            sys.exit(f'{key} already differs; use update for images, reissue TLS for IP changes')
    missing = [(key, val) for key, val in [('PUBLIC_IP', ip), ('VAULT_IMAGE_TAG', tag)] if key not in values]
    with env.open('a') as stream:
        for key, val in missing:
            stream.write(f'\n{key}={val}\n')
else:
    values = {'PUBLIC_IP': ip, 'VAULT_IMAGE_TAG': tag}
    values.update({key: secrets.token_hex(24) for key in (
        'AUTH_DB_PASSWORD', 'CHAT_DB_PASSWORD', 'MEDIA_DB_PASSWORD', 'REDIS_PASSWORD', 'S3_SECRET_KEY')})
    values['S3_ACCESS_KEY'] = 'vault' + secrets.token_hex(8)
    fd = os.open(env, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(''.join(f'{key}={val}\n' for key, val in values.items()))
# Append new least-privilege credentials on upgrade without replacing live keys.
missing_secrets = [key for key in ('AUTH_RUNTIME_DB_PASSWORD', 'CHAT_RUNTIME_DB_PASSWORD',
                                  'MEDIA_RUNTIME_DB_PASSWORD', 'S3_ROOT_ACCESS_KEY', 'S3_ROOT_SECRET_KEY')
                   if key not in values]
if missing_secrets:
    with env.open('a') as stream:
        if env.stat().st_size and not env.read_bytes().endswith(b'\n'):
            stream.write('\n')
        for key in missing_secrets:
            stream.write(f'{key}={secrets.token_hex(24)}\n')
env.chmod(0o600)
subprocess.run(['sh', str(root / 'scripts/gen-jwt-keys.sh'), str(root / 'secrets')], check=True)
subprocess.run(['python3', str(root / 'scripts/gen-rpc-certs.py'), str(root / 'secrets')], check=True)
rpc_env = root / '.env.rpc'
if not rpc_env.exists():
    fd = os.open(rpc_env, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write('RPC_TLS_MODE=mtls\n')
print('Configuration ready; existing credentials and JWT keys are preserved')
