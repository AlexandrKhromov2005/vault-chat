"""Verify our backup format before any extraction as root."""
import hashlib
from pathlib import Path, PurePosixPath
import re
import sys
import tarfile

FILES = {'auth.dump', 'chat.dump', 'media.dump', 'config.tar.gz', 'media-objects.tar.gz', 'images.txt'}


def verify(root):
    root = Path(root)
    if (root / 'COMPLETE').read_text().strip() != '1':
        raise ValueError('Unsupported or incomplete backup')
    entries = {}
    for line in (root / 'SHA256SUMS').read_text().splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([a-z.-]+)', line)
        if not match or match[2] not in FILES or match[2] in entries:
            raise ValueError('Invalid checksum manifest')
        entries[match[2]] = match[1]
    if set(entries) != FILES:
        raise ValueError('Missing backup files')
    for name, expected in entries.items():
        with (root / name).open('rb') as stream:
            actual = hashlib.file_digest(stream, 'sha256').hexdigest()
        if actual != expected:
            raise ValueError(f'Checksum mismatch: {name}')
    for name in ['config.tar.gz', 'media-objects.tar.gz']:
        with tarfile.open(root / name) as archive:
            for member in archive:
                p = PurePosixPath(member.name)
                if p.is_absolute() or '..' in p.parts or not (member.isfile() or member.isdir()):
                    raise ValueError('Unsafe archive member')
                if name == 'config.tar.gz' and not (
                    p.as_posix() in {'.env.vps', 'deployments/docker-compose.vps.yml'}
                    or p.parts[:1] == ('secrets',)
                ):
                    raise ValueError('Unexpected configuration path')
    print('Backup checksums, format and archive paths verified')


if __name__ == '__main__':
    verify(sys.argv[1])
