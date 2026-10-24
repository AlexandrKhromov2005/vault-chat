"""Validate an off-host rclone backend without printing its credentials."""
import configparser
import ipaddress
from pathlib import Path
import re
import sys
from urllib.parse import urlparse


def validate(remote, config):
    match = re.fullmatch(r'([A-Za-z0-9_-]+):(.+)', remote)
    if not match or '..' in match[2].split('/'):
        raise ValueError('BACKUP_REMOTE must be an rclone remote:path')
    path = Path(config)
    if path.stat().st_mode & 0o077:
        raise ValueError('rclone configuration must be readable only by its owner')
    parser = configparser.ConfigParser(interpolation=None)
    parser.read(path)
    settings = parser[match[1]]
    if settings.get('type') not in ('sftp', 's3'):
        raise ValueError('Backups require an external SFTP or S3 backend')
    if settings['type'] == 'sftp' and not settings.get('known_hosts_file'):
        raise ValueError('SFTP backups require known_hosts_file for server authentication')
    if settings['type'] == 's3' and settings.get('endpoint') and urlparse(settings['endpoint']).scheme != 'https':
        raise ValueError('S3 backups require HTTPS')
    host = settings.get('host', '') if settings['type'] == 'sftp' else urlparse(settings.get('endpoint', '')).hostname
    if host:
        if host.lower() == 'localhost':
            raise ValueError('The backup endpoint must be off-host')
        try:
            address = ipaddress.ip_address(host)
        except ValueError:
            address = None
        if address and (address.is_loopback or address.is_unspecified):
            raise ValueError('The backup endpoint must be off-host')


if __name__ == '__main__':
    validate(sys.argv[1], sys.argv[2])
