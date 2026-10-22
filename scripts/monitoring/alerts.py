"""Loopback-only Alertmanager receiver; emit a bounded record to systemd journal."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os


def sanitize(payload):
    alerts = payload.get('alerts', [])
    if not isinstance(alerts, list) or len(alerts) > 100:
        raise ValueError('Invalid alert batch')
    result = []
    for alert in alerts:
        labels, annotations = alert.get('labels', {}), alert.get('annotations', {})
        record = {key: str(labels.get(key, ''))[:512] for key in ['alertname', 'service', 'job', 'instance', 'severity']}
        record.update(status=str(alert.get('status', payload.get('status', '')))[:32], summary=str(annotations.get('summary', ''))[:512])
        result.append(record)
    return result


class Handler(BaseHTTPRequestHandler):
    def setup(self):
        super().setup()
        self.connection.settimeout(5)

    def do_GET(self):
        if self.path != "/health":
            self.send_error(404)
            return
        self.send_response(200)
        self.end_headers()

    def do_POST(self):
        if self.path != '/alerts':
            self.send_error(404)
            return
        try:
            length = int(self.headers.get('Content-Length', '0'))
            if not 0 < length <= 262144:
                raise ValueError('Invalid length')
            records = sanitize(json.loads(self.rfile.read(length)))
            for record in records:
                print(json.dumps(record, ensure_ascii=False), flush=True)
            self.send_response(200)
            self.end_headers()
        except (ValueError, TypeError, AttributeError, OSError):
            self.send_error(400)

    def log_message(self, *_):
        pass


if __name__ == '__main__':
    ThreadingHTTPServer(('127.0.0.1', int(os.environ.get('ALERT_JOURNAL_PORT', '9080'))), Handler).serve_forever()
