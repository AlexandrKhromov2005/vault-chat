#!/usr/bin/env python3
"""Local disposable acceptance for the isolated preview containers."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('rpc_certs', ROOT / 'scripts/gen-rpc-certs.py')
certs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(certs)


def main():
    endpoint = subprocess.check_output(
        ['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], text=True,
    ).strip()
    if not endpoint.startswith('unix://'):
        raise RuntimeError('preview acceptance requires a local Unix Docker endpoint')
    project = f'vault-preview-{os.getpid()}-{time.time_ns()}'
    tag = f'acceptance-{os.getpid()}-{time.time_ns()}'
    with tempfile.TemporaryDirectory(prefix='vault-preview-') as directory:
        temporary = Path(directory)
        certs.issue(temporary / 'secrets')
        env = dict(
            os.environ,
            COMPOSE_PROJECT_NAME=project,
            PREVIEW_RPC_CERTS_DIR=str(temporary / 'secrets/rpc/preview'),
            VAULT_IMAGE_TAG=tag,
        )
        base = ['docker', 'compose', '-p', project, '-f', str(ROOT / 'deployments/docker-compose.preview.yml')]
        oom = base + ['-f', str(ROOT / 'tests/compose/preview_oom.yml')]

        def run(command, check=True, timeout=120):
            result = subprocess.run(command, cwd=ROOT, env=env, text=True, capture_output=True, timeout=timeout)
            if check and result.returncode:
                raise RuntimeError(f"command failed ({result.returncode}): {' '.join(command)}\n{result.stdout}\n{result.stderr}")
            return result

        def client(service, identity='media', image=None, check=True):
            command = [
                'docker', 'run', '--rm', '--network', f'{project}_preview',
                '--user', '65532:65532', '--read-only', '--cap-drop', 'ALL',
                '--security-opt', 'no-new-privileges', '--memory', '128m', '--memory-swap', '128m',
                '--pids-limit', '32', '-e', 'RPC_TLS_MODE=mtls', '-e', 'RPC_TLS_DIR=/run/secrets/rpc',
                '-e', f'RPC_TLS_SERVICE={identity}',
                '--mount', f"type=bind,src={temporary / 'secrets/rpc' / identity},dst=/run/secrets/rpc,readonly",
            ]
            if image:
                command += ['--mount', f'type=bind,src={image},dst=/run/fixture.jpg,readonly']
            command += [f'vault-chat/preview:{tag}', '-check', f'{service}:50054']
            if image:
                command += ['-image', '/run/fixture.jpg']
            return run(command, check=check, timeout=60)

        try:
            run(base + ['up', '-d', '--build', '--wait', 'media-preview-1', 'media-preview-2'], timeout=900)
            for service in ('media-preview-1', 'media-preview-2'):
                container = run(base + ['ps', '-q', service]).stdout.strip()
                if not container:
                    raise RuntimeError(f'no container for {service}')
                inspect = json.loads(run(['docker', 'inspect', container]).stdout)[0]
                host = inspect['HostConfig']
                if inspect['Config']['User'] != '65532:65532' or not host['ReadonlyRootfs']:
                    raise RuntimeError(f'{service}: non-root/read-only policy missing')
                if host['CapDrop'] != ['ALL'] or host['PidsLimit'] != 16:
                    raise RuntimeError(f'{service}: capability/PID budget missing')
                if host['Memory'] != 192 * 1024 * 1024 or host['MemorySwap'] != 192 * 1024 * 1024:
                    raise RuntimeError(f'{service}: memory/swap budget missing')
                if host['NanoCpus'] != 1_000_000_000 or 'no-new-privileges:true' not in host['SecurityOpt']:
                    raise RuntimeError(f'{service}: CPU/privilege budget missing')
                if inspect['NetworkSettings']['Ports']:
                    raise RuntimeError(f'{service}: published port found')
                mounts = inspect['Mounts']
                if len(mounts) != 1 or mounts[0]['Destination'] != '/run/secrets/rpc' or mounts[0]['RW']:
                    raise RuntimeError(f'{service}: unexpected mounts')
            network = json.loads(run(['docker', 'network', 'inspect', f'{project}_preview']).stdout)[0]
            if not network['Internal'] or len(network['Containers']) != 2:
                raise RuntimeError('preview network is not isolated to the two workers')
            client('media-preview-1')
            client('media-preview-2')
            denied = client('media-preview-1', identity='gateway', check=False)
            if denied.returncode == 0:
                raise RuntimeError('Gateway identity reached Preview.Decode')
            print('mTLS decode, ACL and container isolation: OK')

            run(oom + ['up', '-d', '--wait', 'media-preview-1'], timeout=180)
            fixture = temporary / 'photo-24mp.jpg'
            run(['go', 'run', './tests/compose/preview-fixture', str(fixture)], timeout=180)
            fixture.chmod(0o644)
            failed = client('media-preview-1', image=fixture, check=False)
            if failed.returncode == 0:
                raise RuntimeError('64 MiB worker unexpectedly decoded the 24 MP JPEG')
            client('media-preview-2', image=fixture)
            for attempt in range(20):
                healthy = run(base + ['exec', '-T', 'media-preview-1', '/usr/local/bin/service', '-health', '127.0.0.1:50054'], check=False)
                if healthy.returncode == 0:
                    break
                time.sleep(0.5)
            else:
                raise RuntimeError('supervisor did not survive decoder OOM')
            client('media-preview-1')
            print('decoder OOM is isolated; second worker and recovery: OK')
        finally:
            run(oom + ['down', '-v', '--remove-orphans'], check=False, timeout=120)
            run(['docker', 'image', 'rm', f'vault-chat/preview:{tag}'], check=False)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.SubprocessError) as error:
        sys.exit(f'preview acceptance failed: {error}')
