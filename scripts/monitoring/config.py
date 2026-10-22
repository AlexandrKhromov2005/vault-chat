"""Render private monitoring configuration using only the Python standard library."""
import ipaddress
import json
import os
from pathlib import Path
import secrets
import sys
import time
import urllib.request

PORTS = {'PROMETHEUS_PORT': 9090, 'ALERTMANAGER_PORT': 9093, 'GRAFANA_PORT': 3000,
         'NODE_EXPORTER_PORT': 9100, 'BLACKBOX_PORT': 9115, 'ALERT_JOURNAL_PORT': 9080,
         'AUTH_METRICS_HOST_PORT': 9101, 'CHAT_METRICS_HOST_PORT': 9102,
         'MEDIA_METRICS_HOST_PORT': 9103, 'GATEWAY_METRICS_HOST_PORT': 9104}


def values(path):
    return dict(line.split('=', 1) for line in path.read_text().splitlines()
                if line and not line.startswith('#') and '=' in line)


def settings(root):
    result = dict(PORTS)
    for key, value in values(root / '.env.monitoring').items():
        if key not in PORTS:
            raise ValueError(f'Unknown monitoring setting: {key}')
        result[key] = int(value)
    if any(not 1024 <= port <= 65535 for port in result.values()) or len(set(result.values())) != len(result):
        raise ValueError('Monitoring ports must be distinct and between 1024 and 65535')
    return result


def write_json(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix('.next')
    temporary.write_text(json.dumps(data, indent=2) + '\n')
    temporary.chmod(0o644)
    temporary.replace(path)


def render(root):
    root = Path(root)
    env = root / '.env.monitoring'
    if not env.exists():
        fd = os.open(env, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as stream:
            stream.write(''.join(f'{key}={value}\n' for key, value in PORTS.items()))
    env.chmod(0o600)
    ports = settings(root)
    ip = str(ipaddress.IPv4Address(values(root / '.env.vps')['PUBLIC_IP']))
    password = root / 'secrets/grafana_admin_password'
    password.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if not password.exists():
        fd = os.open(password, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as stream:
            stream.write(secrets.token_urlsafe(32) + '\n')
    runtime = root / 'deployments/monitoring/runtime'
    jobs = []
    for name, key in [('auth', 'AUTH_METRICS_HOST_PORT'), ('chat', 'CHAT_METRICS_HOST_PORT'),
                      ('media', 'MEDIA_METRICS_HOST_PORT'), ('gateway', 'GATEWAY_METRICS_HOST_PORT'),
                      ('node', 'NODE_EXPORTER_PORT'), ('alertmanager', 'ALERTMANAGER_PORT'),
                      ('grafana', 'GRAFANA_PORT'), ('blackbox', 'BLACKBOX_PORT'), ('prometheus', 'PROMETHEUS_PORT')]:
        jobs.append({'job_name': name, 'sample_limit': 15000, 'body_size_limit': '8MB',
                     'static_configs': [{'targets': [f'127.0.0.1:{ports[key]}'], 'labels': {'service': name}}]})
    for name, module, targets in [('gateway-ready', 'https_ready', [f'https://{ip}/ready']),
                                  ('storage-ready', 'http_ready', ['http://minio:9000/minio/health/ready']),
                                  ('data-tcp', 'tcp', ['redis:6379', 'postgres:5432', 'chat-postgres:5432', 'media-postgres:5432'])]:
        jobs.append({'job_name': name, 'metrics_path': '/probe', 'params': {'module': [module]},
                     'static_configs': [{'targets': targets}], 'relabel_configs': [
                         {'source_labels': ['__address__'], 'target_label': '__param_target'},
                         {'source_labels': ['__param_target'], 'target_label': 'instance'},
                         {'target_label': '__address__', 'replacement': f'127.0.0.1:{ports["BLACKBOX_PORT"]}'}]})
    write_json(runtime / 'prometheus.yml', {'global': {'scrape_interval': '15s', 'scrape_timeout': '7s', 'evaluation_interval': '15s'},
        'rule_files': ['/etc/prometheus/alerts.yml'], 'alerting': {'alertmanagers': [
            {'static_configs': [{'targets': [f'127.0.0.1:{ports["ALERTMANAGER_PORT"]}']}]}]}, 'scrape_configs': jobs})
    receiver = {'name': 'notifications', 'webhook_configs': [{'url': f'http://127.0.0.1:{ports["ALERT_JOURNAL_PORT"]}/alerts', 'send_resolved': True, 'max_alerts': 50}]}
    private = root / 'secrets/monitoring'
    private.mkdir(mode=0o700, parents=True, exist_ok=True)
    token, chat = private / 'telegram_token', private / 'telegram_chat_id'
    if token.exists() != chat.exists():
        raise ValueError('Telegram requires both telegram_token and telegram_chat_id files')
    if token.exists():
        chat_id = int(chat.read_text().strip())
        if not chat_id or not -(2**63) <= chat_id < 2**63 or not token.read_text().strip():
            raise ValueError('Invalid Telegram settings')
        receiver['telegram_configs'] = [{'bot_token_file': '/run/secrets/monitoring/telegram_token',
            'chat_id': chat_id, 'send_resolved': True, 'message': '{{ .Status }}: {{ .CommonLabels.alertname }}\n{{ .CommonAnnotations.summary }}'}]
    write_json(runtime / 'alertmanager.yml', {'route': {'receiver': 'notifications', 'group_by': ['alertname', 'job', 'service'],
        'group_wait': '10s', 'group_interval': '30s', 'repeat_interval': '4h'}, 'receivers': [receiver]})
    write_json(runtime / 'datasources/default.yml', {'apiVersion': 1, 'datasources': [
        {'name': 'Prometheus', 'uid': 'vault-prometheus', 'type': 'prometheus', 'access': 'proxy',
         'url': f'http://127.0.0.1:{ports["PROMETHEUS_PORT"]}', 'isDefault': True, 'editable': False},
        {'name': 'Alertmanager', 'uid': 'vault-alertmanager', 'type': 'alertmanager', 'access': 'proxy',
         'url': f'http://127.0.0.1:{ports["ALERTMANAGER_PORT"]}', 'jsonData': {'implementation': 'prometheus'}, 'editable': False}]})
    print('Monitoring configuration rendered; credentials preserved')


def ready(root):
    ports = settings(root)
    pending = {'PROMETHEUS_PORT': '/-/ready', 'ALERTMANAGER_PORT': '/-/ready', 'GRAFANA_PORT': '/api/health',
               'BLACKBOX_PORT': '/-/healthy', 'NODE_EXPORTER_PORT': '/metrics'}
    for _ in range(60):
        for key, path in list(pending.items()):
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{ports[key]}{path}', timeout=2) as response:
                    if response.status == 200:
                        del pending[key]
            except OSError:
                pass
        if not pending:
            print('All monitoring endpoints ready')
            return
        time.sleep(2)
    raise RuntimeError('Monitoring endpoints not ready: ' + ', '.join(pending))


if __name__ == '__main__':
    (ready if len(sys.argv) > 2 and sys.argv[2] == 'ready' else render)(Path(sys.argv[1]))
