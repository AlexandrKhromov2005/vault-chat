#!/usr/bin/env python3
"""Local disposable acceptance for the push signal broker of the push overlay.

Starts only the broker from deployments/docker-compose.push.yml with
synthetic certificates, checks its container policy, stores a signal as Chat,
restarts the container and receives the signal as Auth. Every resource
belongs to a unique Compose project and is removed at the end.
"""
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
        raise RuntimeError('push broker acceptance requires a local Unix Docker endpoint')
    project = f'vault-push-{os.getpid()}-{time.time_ns()}'
    with tempfile.TemporaryDirectory(prefix='vault-push-') as directory:
        temporary = Path(directory)
        certs.issue(temporary / 'secrets')
        env = dict(os.environ, COMPOSE_PROJECT_NAME=project, NATS_RPC_CERTS_DIR=str(temporary / 'secrets/rpc/nats'))
        deployed = ['docker', 'compose', '-p', project, '-f', str(ROOT / 'deployments/docker-compose.yml'),
                    '-f', str(ROOT / 'deployments/docker-compose.push.yml')]
        reachable = deployed + ['-f', str(ROOT / 'tests/compose/push_broker.yml')]

        def run(command, check=True, timeout=120, extra=None):
            result = subprocess.run(command, cwd=ROOT, env=dict(env, **(extra or {})), text=True,
                                    capture_output=True, timeout=timeout)
            if check and result.returncode:
                raise RuntimeError(f"command failed ({result.returncode}): {' '.join(command)}\n{result.stdout}\n{result.stderr}")
            return result

        def phase(name, url):
            run(['go', 'test', '-tags', 'compose', '-count=1', '-run', 'TestPushBroker', './tests/compose/'], timeout=300,
                extra={'VAULT_CHAT_COMPOSE_NATS_URL': url, 'VAULT_CHAT_COMPOSE_RPC_CERTS': str(temporary / 'secrets/rpc'),
                       'VAULT_CHAT_COMPOSE_PUSH_PHASE': name})

        try:
            # The deployed definition publishes nothing.
            config = json.loads(run(deployed + ['config', '--format', 'json']).stdout)
            if config['services']['nats'].get('ports'):
                raise RuntimeError('the push overlay publishes a broker port')
            run(reachable + ['up', '-d', '--wait', 'nats'], timeout=300)
            container = run(reachable + ['ps', '-q', 'nats']).stdout.strip()
            inspect = json.loads(run(['docker', 'inspect', container]).stdout)[0]
            host = inspect['HostConfig']
            if inspect['Config']['User'] != '65532:65532' or not host['ReadonlyRootfs'] or host['CapDrop'] != ['ALL']:
                raise RuntimeError('broker: non-root, read-only or capability policy missing')
            if 'no-new-privileges:true' not in host['SecurityOpt'] or host['Memory'] != 128 * 1024 * 1024:
                raise RuntimeError('broker: privilege or memory budget missing')
            mounts = {m['Destination']: m for m in inspect['Mounts']}
            if set(mounts) != {'/data', '/etc/nats/nats.conf', '/run/secrets/nats'} or mounts['/run/secrets/nats']['RW'] \
                    or mounts['/etc/nats/nats.conf']['RW']:
                raise RuntimeError('broker: unexpected mounts')
            network = json.loads(run(['docker', 'network', 'inspect', f'{project}_push-signals']).stdout)[0]
            if not network['Internal']:
                raise RuntimeError('push-signals network is not internal')
            port = run(reachable + ['port', 'nats', '4222']).stdout.strip().rsplit(':', 1)[1]
            url = f'tls://127.0.0.1:{port}'
            phase('store', url)
            print('broker policy, stream declaration, Chat publish and identity refusals: OK')

            run(reachable + ['restart', 'nats'], timeout=120)
            run(reachable + ['up', '-d', '--wait', 'nats'], timeout=300)
            port = run(reachable + ['port', 'nats', '4222']).stdout.strip().rsplit(':', 1)[1]
            phase('keep', f'tls://127.0.0.1:{port}')
            print('signal kept on the broker volume across a restart and delivered to Auth: OK')
        finally:
            run(reachable + ['down', '-v', '--remove-orphans'], check=False, timeout=120)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.SubprocessError) as error:
        sys.exit(f'push broker acceptance failed: {error}')
