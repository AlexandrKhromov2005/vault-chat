#!/usr/bin/env python3
"""Issue a private deployment CA and separate service certificates using OpenSSL.

Usage: gen-rpc-certs.py SECRETS_DIR [--rotate]
Rotation keeps the CA and replaces all leaves. Stop applications before rotation.
The signing key is never placed in a service's mounted directory.
"""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

SERVICES = ('auth', 'chat', 'media', 'gateway')


def openssl(*args):
    return subprocess.check_output(['openssl', *map(str, args)], stderr=subprocess.PIPE)


def issue(directory, rotate=False):
    root = Path(directory)
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    ca = root / 'rpc-ca'
    runtime = root / 'rpc'
    if ca.exists() and not all((ca / n).is_file() for n in ['ca.pem', 'ca.key']):
        raise ValueError('Incomplete RPC CA; refusing to replace it')
    existing = runtime.exists()
    if existing and not ca.exists():
        raise ValueError('RPC leaves exist without their CA; refusing to replace it')
    if existing and not rotate:
        for service in SERVICES:
            leaf = runtime / service
            for name in ['ca.pem', 'tls.crt', 'tls.key']:
                if not (leaf / name).is_file():
                    raise ValueError('Incomplete RPC certificate set; use --rotate after inspection')
            if (leaf / 'ca.pem').read_bytes() != (ca / 'ca.pem').read_bytes():
                raise ValueError('RPC trust bundle differs from deployment CA')
            openssl('verify', '-CAfile', ca / 'ca.pem', '-verify_hostname', service + '.vault.internal', leaf / 'tls.crt')
            if openssl('pkey', '-in', leaf / 'tls.key', '-pubout') != openssl('x509', '-in', leaf / 'tls.crt', '-pubkey', '-noout'):
                raise ValueError('RPC certificate/key mismatch')
        print('Existing RPC CA and certificates preserved')
        return
    with tempfile.TemporaryDirectory(prefix='.rpc-', dir=root) as temporary:
        staging = Path(temporary)
        if not ca.exists():
            candidate = staging / 'ca'
            candidate.mkdir(mode=0o700)
            openssl('req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-days', '3650',
                    '-subj', '/CN=Vault Chat internal CA', '-addext', 'basicConstraints=critical,CA:TRUE,pathlen:0',
                    '-addext', 'keyUsage=critical,keyCertSign,cRLSign', '-keyout', candidate / 'ca.key', '-out', candidate / 'ca.pem')
            (candidate / 'ca.key').chmod(0o600)
            candidate.rename(ca)
        generated = staging / 'rpc'
        generated.mkdir()
        for service in SERVICES:
            leaf = generated / service
            leaf.mkdir()
            config = leaf / 'extensions.cnf'
            config.write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth,clientAuth\n'
                              f'subjectAltName=DNS:{service}.vault.internal,URI:spiffe://vault-chat/service/{service}\n')
            openssl('req', '-new', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes',
                    '-subj', '/CN=' + service, '-keyout', leaf / 'tls.key', '-out', leaf / 'request.csr')
            openssl('x509', '-req', '-in', leaf / 'request.csr', '-CA', ca / 'ca.pem', '-CAkey', ca / 'ca.key',
                    '-set_serial', str(int.from_bytes(os.urandom(20), 'big') >> 1 or 1), '-days', '90',
                    '-extfile', config, '-out', leaf / 'tls.crt')
            config.unlink()
            (leaf / 'request.csr').unlink()
            shutil.copyfile(ca / 'ca.pem', leaf / 'ca.pem')
        runtime.mkdir(exist_ok=True)
        for service in SERVICES:
            destination = runtime / service
            destination.mkdir(exist_ok=True)
            for name in ['ca.pem', 'tls.key', 'tls.crt']:
                source = generated / service / name
                source.chmod(0o400 if os.geteuid() == 0 and name == 'tls.key' else 0o644)
                if os.geteuid() == 0:
                    os.chown(source, 65532, 65532)
                source.replace(destination / name)
            destination.chmod(0o700 if os.geteuid() == 0 else 0o755)
            if os.geteuid() == 0:
                os.chown(destination, 65532, 65532)
    print('RPC certificates issued; CA signing key remains outside service mounts')


if __name__ == '__main__':
    if len(sys.argv) not in (2, 3) or (len(sys.argv) == 3 and sys.argv[2] != '--rotate'):
        sys.exit('Usage: gen-rpc-certs.py SECRETS_DIR [--rotate]')
    try:
        issue(sys.argv[1], len(sys.argv) == 3)
    except (ValueError, subprocess.CalledProcessError) as error:
        sys.exit('RPC certificate generation failed: ' + (str(error) if isinstance(error, ValueError) else 'OpenSSL validation failed'))
