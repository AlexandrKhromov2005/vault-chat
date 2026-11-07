"""Monitoring secrets, alert privacy and operational signals."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]

def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts/monitoring' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class Monitoring(unittest.TestCase):
    def test_render_preserves_password_and_keeps_secrets_out_of_configs(self):
        config = load('config')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / '.env.vps').write_text('PUBLIC_IP=168.113.171.56\n')
            config.render(root)
            password = (root / 'secrets/grafana_admin_password').read_text()
            config.render(root)
            self.assertEqual(password, (root / 'secrets/grafana_admin_password').read_text())
            for path in (root / 'deployments/monitoring/runtime').rglob('*.yml'):
                self.assertNotIn(password.strip(), path.read_text())
            self.assertEqual((root / '.env.monitoring').stat().st_mode & 0o777, 0o600)
            with (root / '.env.monitoring').open('a') as stream:
                stream.write('GRAFANA_PORT=0\n')
            with self.assertRaises(ValueError):
                config.render(root)

    def test_render_uses_fixed_private_addresses_and_migrates_existing_ports(self):
        config = load('config')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / '.env.vps').write_text('PUBLIC_IP=168.113.171.56\n')
            (root / '.env.monitoring').write_text(
                'GRAFANA_PORT=13000\nAUTH_METRICS_HOST_PORT=9101\nCHAT_METRICS_HOST_PORT=9102\n'
                'MEDIA_METRICS_HOST_PORT=9103\nGATEWAY_METRICS_HOST_PORT=9104\n')
            config.render(root)
            settings = config.settings(root)
            self.assertEqual(settings['GRAFANA_PORT'], 13000)
            self.assertEqual(settings['AUTH_METRICS_IPV4_ADDRESS'], '172.31.240.2')
            prometheus = json.loads((root / 'deployments/monitoring/runtime/prometheus.yml').read_text())
            jobs = {job['job_name']: job for job in prometheus['scrape_configs']}
            self.assertEqual(jobs['auth']['static_configs'][0]['targets'], ['172.31.240.2:9100'])
            self.assertEqual(jobs['chat']['static_configs'][0]['targets'], ['172.31.240.10:9100'])
            self.assertEqual(jobs['data-tcp']['static_configs'][0]['targets'], [
                '172.31.240.58:6379', '172.31.240.34:5432',
                '172.31.240.42:5432', '172.31.240.50:5432'])
            self.assertEqual(jobs['storage-ready']['static_configs'][0]['targets'], [
                'http://172.31.240.66:9000/minio/health/ready'])

    def test_network_overlap_is_rejected_except_for_own_exact_network(self):
        config = load('config')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / '.env.vps').write_text('PUBLIC_IP=168.113.171.56\n')
            config.render(root)
            settings = config.settings(root)
            config.check_networks(settings, [{'Name': 'vault-chat_metrics-auth', 'IPAM': {
                'Config': [{'Subnet': '172.31.240.0/29'}]}}], 'vault-chat')
            with self.assertRaises(ValueError):
                config.check_networks(settings, [{'Name': 'unrelated', 'IPAM': {
                    'Config': [{'Subnet': '172.31.240.0/28'}]}}], 'vault-chat')
            with (root / '.env.monitoring').open('a') as stream:
                stream.write('CHAT_METRICS_SUBNET=172.31.240.0/29\n')
            with self.assertRaises(ValueError):
                config.settings(root)

    def test_alert_journal_does_not_log_unknown_labels_or_payload(self):
        alerts = load('alerts')
        result = alerts.sanitize({'status': 'firing', 'password': 'secret', 'alerts': [
            {'status': 'firing', 'labels': {'alertname': 'ServiceMetricsUnavailable', 'service': 'auth',
             'severity': 'critical', 'user_id': 'private-id'}, 'annotations': {'summary': 'Service down', 'token': 'secret'}}]})
        self.assertNotIn('secret', json.dumps(result))
        self.assertNotIn('private-id', json.dumps(result))
        self.assertEqual(result[0]['alertname'], 'ServiceMetricsUnavailable')

    def test_operations_ignores_incomplete_backups(self):
        operations = load('operations')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            complete = root / '20261022T010000Z'
            complete.mkdir()
            (complete / 'COMPLETE').write_text('1\n')
            (root / '20261022T020000Z').mkdir()
            self.assertEqual(operations.last_backup(root), 1792630800)
