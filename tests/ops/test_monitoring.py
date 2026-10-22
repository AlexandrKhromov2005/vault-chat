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
