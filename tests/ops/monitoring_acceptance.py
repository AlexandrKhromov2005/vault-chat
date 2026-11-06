#!/usr/bin/env python3
"""Disposable local Docker acceptance for the monitoring trust boundary."""
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
PYTHON_IMAGE = 'python:3.12-slim@sha256:57cd7c3a7a273101a6485ba99423ee568157882804b1124b4dd04266317710de'
PROMETHEUS_IMAGE = 'prom/prometheus@sha256:fa3302279710be2f21a8301ec17a8b4f2301fd829bd113839b3b6b02b6f191a2'
BLACKBOX_IMAGE = 'prom/blackbox-exporter@sha256:ea1c7aa3ef3f307cd03e85f34abb604478ccb90b567fc690da0cd9776da76b13'


def run(*args, capture=False):
    result = subprocess.run(args, text=True, capture_output=capture)
    if result.returncode:
        raise RuntimeError(f'Command failed ({result.returncode}): {args}\n{result.stderr}')
    return result.stdout.strip() if capture else ''


def load_config():
    spec = importlib.util.spec_from_file_location('monitoring_config', ROOT / 'scripts/monitoring/config.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def free_port():
    with socket.socket() as stream:
        stream.bind(('127.0.0.1', 0))
        return stream.getsockname()[1]


def wait_for(description, check, timeout=90):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            result = check()
            if result:
                return result
            last = 'condition is false'
        except (OSError, ValueError, subprocess.CalledProcessError) as error:
            last = str(error)
        time.sleep(2)
    raise RuntimeError(f'{description}: {last}')


def json_url(url):
    with urllib.request.urlopen(url, timeout=3) as response:
        return json.load(response)


def main():
    endpoint = run('docker', 'context', 'inspect', '--format',
                   '{{.Endpoints.docker.Host}}', capture=True)
    if not endpoint.startswith('unix://'):
        raise RuntimeError(f'Refusing non-local Docker endpoint: {endpoint}')
    config_module = load_config()
    project = f'vault-monitor-{os.getpid()}'
    created_containers = []
    created_networks = []
    created_volumes = []
    with tempfile.TemporaryDirectory(prefix='vault-monitoring-acceptance-') as directory:
        fixture = Path(directory) / 'fixture'
        fixture.mkdir(mode=0o755)
        (fixture / 'server.py').write_text((ROOT / 'tests/ops/fixtures/monitoring_server.py').read_text())
        (fixture / 'server.py').chmod(0o644)
        root = Path(directory) / 'root'
        root.mkdir()
        (root / '.env.vps').write_text('PUBLIC_IP=172.31.240.26\n')
        ports = {key: free_port() for key in ['PROMETHEUS_PORT', 'BLACKBOX_PORT', 'ALERTMANAGER_PORT',
                 'GRAFANA_PORT', 'NODE_EXPORTER_PORT', 'ALERT_JOURNAL_PORT']}
        (root / '.env.monitoring').write_text(''.join(f'{key}={value}\n' for key, value in ports.items()))
        os.environ['VAULT_PROJECT'] = project
        config_module.render(root)
        config = config_module.settings(root)
        config_module.check_docker_networks(root)
        try:
            for key, name in config_module.NETWORK_NAMES.items():
                network = f'{project}_{name}'
                run('docker', 'network', 'create', '--internal', '--subnet', config[key], network)
                created_networks.append(network)
            run('docker', 'network', 'create', '--internal', f'{project}_rpc')
            created_networks.append(f'{project}_rpc')

            def container(name, image, args, network=None, address=None, extra=()):
                command = ['docker', 'run', '-d', '--name', f'{project}-{name}',
                           '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                           '--pids-limit', '64', '--tmpfs', '/tmp:rw,nosuid,nodev,size=16m',
                           '-e', 'PYTHONDONTWRITEBYTECODE=1']
                if network:
                    command.extend(['--network', network])
                if address:
                    command.extend(['--ip', address])
                command.extend(extra)
                command.extend([image, *args])
                container_id = run(*command, capture=True)
                created_containers.append(container_id)
                return container_id

            def connect(name, *networks):
                for network in networks:
                    run('docker', 'network', 'connect', f'{project}_{network}', f'{project}-{name}')

            metrics = {
                'auth': ('AUTH_METRICS_IPV4_ADDRESS', 'metrics-auth', 9100),
                'chat': ('CHAT_METRICS_IPV4_ADDRESS', 'metrics-chat', 9100),
                'media': ('MEDIA_METRICS_IPV4_ADDRESS', 'metrics-media', 9100),
            }
            for name, (key, network, port) in metrics.items():
                container(name, PYTHON_IMAGE, ['python', '/fixture/server.py', config[key], str(port)],
                          f'{project}_{network}', config[key],
                          ('--memory', '64m', '-v', f'{fixture}:/fixture:ro'))
            gateway_address = config['GATEWAY_METRICS_IPV4_ADDRESS']
            run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                '-subj', '/CN=monitoring-acceptance.invalid', '-addext', f'subjectAltName=IP:{gateway_address}',
                '-keyout', str(fixture / 'tls.key'), '-out', str(fixture / 'tls.crt'))
            (fixture / 'tls.key').chmod(0o644)
            container('gateway', PYTHON_IMAGE,
                      ['python', '/fixture/server.py', gateway_address, '9100', '--https-port', '443'],
                      f'{project}_metrics-gateway', gateway_address,
                      ('--memory', '64m', '--cap-add', 'NET_BIND_SERVICE', '-v', f'{fixture}:/fixture:ro'))

            dependencies = [
                ('redis', 'REDIS_IPV4_ADDRESS', 'events', 6379),
                ('postgres', 'POSTGRES_IPV4_ADDRESS', 'auth-data', 5432),
                ('chat-postgres', 'CHAT_POSTGRES_IPV4_ADDRESS', 'chat-data', 5432),
                ('media-postgres', 'MEDIA_POSTGRES_IPV4_ADDRESS', 'media-data', 5432),
                ('minio', 'MINIO_IPV4_ADDRESS', 'objects', 9000),
            ]
            for name, key, network, port in dependencies:
                container(name, PYTHON_IMAGE, ['python', '/fixture/server.py', config[key], str(port)],
                          f'{project}_{network}', config[key],
                          ('--memory', '64m', '-v', f'{fixture}:/fixture:ro'))
            connect('chat', 'rpc', 'chat-data', 'events')

            prometheus_path = root / 'deployments/monitoring/runtime/prometheus.yml'
            prometheus = json.loads(prometheus_path.read_text())
            prometheus['global'] = {'scrape_interval': '3s', 'scrape_timeout': '2s', 'evaluation_interval': '3s'}
            prometheus['scrape_configs'] = [job for job in prometheus['scrape_configs']
                if job['job_name'] in {'auth', 'chat', 'media', 'gateway', 'gateway-ready', 'storage-ready', 'data-tcp'}]
            prometheus.pop('alerting')
            prometheus_path.write_text(json.dumps(prometheus))
            alerts_path = root / 'deployments/monitoring/runtime/alerts.yml'
            alerts_path.write_text(alerts_path.read_text().replace('for: 2m', 'for: 20s').replace('for: 5m', 'for: 30s'))
            blackbox_path = fixture / 'blackbox.yml'
            blackbox_path.write_text((ROOT / 'deployments/monitoring/blackbox.yml').read_text().replace(
                '      fail_if_not_ssl: true', '      fail_if_not_ssl: true\n      tls_config: {insecure_skip_verify: true}'))
            volume = run('docker', 'volume', 'create', f'{project}-prometheus', capture=True)
            created_volumes.append(volume)
            runtime = root / 'deployments/monitoring/runtime'
            container('prometheus', PROMETHEUS_IMAGE, [
                '--config.file=/etc/prometheus/prometheus.yml', f'--web.listen-address=127.0.0.1:{config["PROMETHEUS_PORT"]}',
                '--storage.tsdb.path=/prometheus', '--storage.tsdb.retention.time=1h', '--query.max-concurrency=2'],
                'host', None, ('--memory', '192m', '-v', f'{runtime}:/etc/prometheus:ro',
                               '-v', f'{volume}:/prometheus'))
            container('blackbox', BLACKBOX_IMAGE, [
                '--config.file=/etc/blackbox/blackbox.yml', f'--web.listen-address=127.0.0.1:{config["BLACKBOX_PORT"]}'],
                'host', None, ('--memory', '64m', '-v', f'{blackbox_path}:/etc/blackbox/blackbox.yml:ro'))

            metrics['gateway'] = ('GATEWAY_METRICS_IPV4_ADDRESS', 'metrics-gateway', 9100)
            for name, (key, _network, port) in metrics.items():
                address = config[key]
                wait_for(f'host metrics {name}', lambda a=address, p=port: urllib.request.urlopen(
                    f'http://{a}:{p}/metrics', timeout=2).status == 200, 30)
            base = f'http://127.0.0.1:{config["PROMETHEUS_PORT"]}'
            wait_for('Prometheus readiness', lambda: urllib.request.urlopen(
                base + '/-/ready', timeout=2).status == 200)
            wait_for('Blackbox readiness', lambda: urllib.request.urlopen(
                f'http://127.0.0.1:{config["BLACKBOX_PORT"]}/-/healthy', timeout=2).status == 200)

            def targets_up():
                targets = json_url(base + '/api/v1/targets?state=active')['data']['activeTargets']
                return len(targets) == 10 and all(target['health'] == 'up' for target in targets)

            wait_for('all application and probe targets up', targets_up, 90)

            def alerts(name, **labels):
                current = json_url(base + '/api/v1/alerts')['data']['alerts']
                return [alert for alert in current if alert['labels'].get('alertname') == name
                        and alert['state'] == 'firing'
                        and all(alert['labels'].get(key) == value for key, value in labels.items())]

            wait_for('healthy probes', lambda: not alerts('DependencyUnavailable'), 30)
            result = subprocess.run(['docker', 'exec', f'{project}-chat', 'python', '-c',
                "import socket; socket.create_connection(('172.31.240.2',9100),1)"], capture_output=True)
            if result.returncode == 0:
                raise RuntimeError('Chat reached Auth metrics through another internal network')
            result = subprocess.run(['docker', 'exec', f'{project}-chat', 'python', '-c',
                "import socket; socket.create_connection(('172.31.240.50',5432),1)"], capture_output=True)
            if result.returncode == 0:
                raise RuntimeError('Chat reached Media PostgreSQL through another internal network')
            result = subprocess.run(['docker', 'exec', f'{project}-chat', 'python', '-c',
                "import socket; socket.create_connection(('192.0.2.1',443),1)"], capture_output=True)
            if result.returncode == 0:
                raise RuntimeError('Internal metrics network unexpectedly has external egress')

            run('docker', 'stop', '-t', '2', f'{project}-chat')
            wait_for('Chat failure alert', lambda: alerts('ServiceMetricsUnavailable', job='chat'), 60)
            run('docker', 'stop', '-t', '2', f'{project}-redis', f'{project}-postgres')
            wait_for('Redis failure alert', lambda: alerts('DependencyUnavailable', instance='172.31.240.58:6379'), 60)
            wait_for('PostgreSQL failure alert', lambda: alerts('DependencyUnavailable', instance='172.31.240.34:5432'), 60)
            run('docker', 'stop', '-t', '2', f'{project}-blackbox')
            wait_for('Blackbox scrape failure alert', lambda: alerts('ServiceMetricsUnavailable', job='data-tcp'), 60)
            print('Monitoring acceptance passed: host access, isolation, probes and failure alerts')
        finally:
            if created_containers:
                subprocess.run(['docker', 'rm', '-f', *created_containers], capture_output=True)
            for network in reversed(created_networks):
                subprocess.run(['docker', 'network', 'rm', network], capture_output=True)
            for volume in created_volumes:
                subprocess.run(['docker', 'volume', 'rm', volume], capture_output=True)


if __name__ == '__main__':
    main()
