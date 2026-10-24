"""Verify our backup format before any extraction as root."""
import hashlib
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tarfile

FILES = {'auth.dump', 'chat.dump', 'media.dump', 'config.tar.gz', 'media-objects.tar.gz', 'images.txt'}


def checksum(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def verify(root):
    root = Path(root)
    version = (root / 'COMPLETE').read_text().strip()
    if version not in ('1', '2'):
        raise ValueError('Unsupported or incomplete backup')
    expected_files = FILES if version == '1' else {name + '.age' if name != 'images.txt' else name for name in FILES}
    entries = {}
    for line in (root / 'SHA256SUMS').read_text().splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([a-z.-]+)', line)
        if not match or match[2] not in expected_files or match[2] in entries:
            raise ValueError('Invalid checksum manifest')
        entries[match[2]] = match[1]
    if set(entries) != expected_files:
        raise ValueError('Missing backup files')
    for name, expected in entries.items():
        if (root / name).is_symlink():
            raise ValueError('Backup files must not be symbolic links')
        with (root / name).open('rb') as stream:
            actual = hashlib.file_digest(stream, 'sha256').hexdigest()
        if actual != expected:
            raise ValueError(f'Checksum mismatch: {name}')
    if version == '2':
        print('Encrypted backup checksums verified; decryption required before extraction')
        return
    for name in ['config.tar.gz', 'media-objects.tar.gz']:
        with tarfile.open(root / name) as archive:
            for member in archive:
                p = PurePosixPath(member.name)
                if p.is_absolute() or '..' in p.parts or not (member.isfile() or member.isdir()):
                    raise ValueError('Unsafe archive member')
                if name == 'config.tar.gz' and not (
                    p.as_posix() in {'.env.vps', '.env.monitoring', '.env.rpc', 'deployments/docker-compose.mtls.yml', 'deployments/docker-compose.vps.yml', 'deployments/monitoring/app.yml'}
                    or p.parts[:1] == ('secrets',)
                ):
                    raise ValueError('Unexpected configuration path')
    print('Backup checksums, format and archive paths verified')


def decrypt(root, destination, identity):
    root, destination = Path(root), Path(destination)
    verify(root)
    for name in sorted(FILES):
        target = destination / name
        if name == 'images.txt':
            target.write_bytes((root / name).read_bytes())
        else:
            subprocess.run(['age', '--decrypt', '--identity', identity, '--output', str(target), str(root / (name + '.age'))], check=True)
    (destination / 'COMPLETE').write_text('1\n')
    (destination / 'SHA256SUMS').write_text(''.join(checksum(destination / name) + '  ' + name + '\n' for name in sorted(FILES)))
    verify(destination)


if __name__ == '__main__':
    if len(sys.argv) == 4:
        decrypt(sys.argv[1], sys.argv[2], sys.argv[3])
    else:
        verify(sys.argv[1])
