"""Publish atomic node_exporter textfile metrics for backup and renewal jobs."""
import datetime
import os
from pathlib import Path
import subprocess
import tempfile
import time


def last_backup(root):
    stamps = []
    for marker in Path(root).glob('*/COMPLETE'):
        if marker.read_text().strip() in ('1', '2'):
            try:
                stamps.append(datetime.datetime.strptime(marker.parent.name, '%Y%m%dT%H%M%SZ').replace(tzinfo=datetime.timezone.utc).timestamp())
            except ValueError:
                continue
    return max(stamps, default=0)


def job_success(name):
    result = subprocess.run(['systemctl', 'show', name, '-p', 'Result', '--value'], capture_output=True, text=True, timeout=5)
    return int(result.returncode == 0 and result.stdout.strip() == 'success')


def collect():
    root = Path(os.environ.get('VAULT_METRICS_DIR', '/var/lib/vault-chat/metrics'))
    root.mkdir(parents=True, exist_ok=True)
    metrics = {'vault_backup_last_success_timestamp_seconds': last_backup(os.environ.get('VAULT_BACKUP_ROOT', '/var/backups/vault-chat')),
               'vault_backup_last_attempt_success': job_success('vault-chat-backup.service'),
               'vault_certbot_last_attempt_success': job_success('vault-certbot.service'),
               'vault_operations_collected_timestamp_seconds': time.time()}
    fd, name = tempfile.mkstemp(dir=root, suffix='.next')
    try:
        with os.fdopen(fd, 'w') as stream:
            for key, value in metrics.items():
                stream.write(f'# TYPE {key} gauge\n{key} {value}\n')
        os.chmod(name, 0o644)
        os.replace(name, root / 'operations.prom')
    finally:
        Path(name).unlink(missing_ok=True)


if __name__ == '__main__':
    collect()
