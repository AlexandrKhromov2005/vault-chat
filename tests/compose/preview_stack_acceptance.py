#!/usr/bin/env python3
"""Disposable full-stack acceptance for authorized asynchronous image previews."""
import importlib.util
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('rpc_certs', ROOT / 'scripts/gen-rpc-certs.py')
certs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(certs)


def port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return str(listener.getsockname()[1])


def main():
    endpoint = subprocess.check_output(
        ['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], text=True,
    ).strip()
    if not endpoint.startswith('unix://'):
        raise RuntimeError('preview stack acceptance requires a local Unix Docker endpoint')
    project = f'vault-preview-stack-{os.getpid()}-{time.time_ns()}'
    with tempfile.TemporaryDirectory(prefix='vault-preview-stack-') as directory:
        temporary = Path(directory)
        jwt = temporary / 'jwt'
        subprocess.run([str(ROOT / 'scripts/gen-jwt-keys.sh'), str(jwt)], check=True, capture_output=True)
        secrets = temporary / 'secrets'
        certs.issue(secrets)
        rpc = secrets / 'rpc'
        for identity in ('auth', 'chat', 'media', 'gateway', 'preview'):
            for name in ('ca.pem', 'tls.crt', 'tls.key'):
                if not (rpc / identity / name).is_file():
                    raise RuntimeError(f'missing synthetic RPC mount: {rpc / identity / name}')
        gateway_port = port()
        auth_port = port()
        env = dict(
            os.environ,
            COMPOSE_PROJECT_NAME=project,
            TEST_JWT_DIR=str(jwt),
            TEST_RPC_DIR=str(rpc),
            PREVIEW_RPC_CERTS_DIR=str(rpc / 'preview'),
            GATEWAY_HOST_PORT=gateway_port,
            POSTGRES_HOST_PORT=auth_port,
            CHAT_POSTGRES_HOST_PORT=port(),
            REDIS_HOST_PORT=port(),
            NATS_HOST_PORT=port(),
            NATS_MONITOR_HOST_PORT=port(),
            MINIO_HOST_PORT=port(),
            MINIO_CONSOLE_HOST_PORT=port(),
            VAULT_IMAGE_TAG=f'acceptance-{os.getpid()}-{time.time_ns()}',
            VAULT_CHAT_COMPOSE_PREVIEW='1',
            VAULT_CHAT_COMPOSE_GATEWAY_URL=f'http://127.0.0.1:{gateway_port}',
            VAULT_CHAT_COMPOSE_AUTH_DATABASE_URL=f'postgres://auth:auth@127.0.0.1:{auth_port}/auth_db?sslmode=disable',
            VAULT_CHAT_WEB_GATEWAY_URL=f'http://127.0.0.1:{gateway_port}',
            VAULT_CHAT_WEB_INVITE_DATABASE_URL=f'postgres://auth:auth@127.0.0.1:{auth_port}/auth_db?sslmode=disable',
        )
        compose = [
            'docker', 'compose', '-p', project,
            '-f', str(ROOT / 'deployments/docker-compose.yml'),
            '-f', str(ROOT / 'deployments/docker-compose.mtls.yml'),
            '-f', str(ROOT / 'deployments/docker-compose.preview.yml'),
            '-f', str(ROOT / 'tests/compose/preview_stack.yml'),
        ]

        def run(command, check=True, timeout=120):
            result = subprocess.run(command, cwd=ROOT, env=env, text=True, capture_output=True, timeout=timeout)
            if check and result.returncode:
                raise RuntimeError(f"command failed ({result.returncode}): {' '.join(command)}\n{result.stdout}\n{result.stderr}")
            return result

        try:
            run(compose + ['up', '-d', '--build', '--wait'], timeout=1200)
            run(['go', 'test', '-tags', 'compose', '-count=1', '-run',
                 'TestCompose_(PrivateAttachments|AuthorizedImagePreview)$', './tests/compose'], timeout=240)
            run(['node', '--test', 'web/test/gateway.test.mjs'], timeout=300)
            print('full stack preview, original download, membership revocation and live web: OK')
        finally:
            run(compose + ['down', '-v', '--rmi', 'local', '--remove-orphans'], check=False, timeout=180)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.SubprocessError) as error:
        sys.exit(f'preview stack acceptance failed: {error}')
