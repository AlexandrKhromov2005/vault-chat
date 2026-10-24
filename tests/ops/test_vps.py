"""Regression checks for data preservation and untrusted backup archives."""
import hashlib
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('archive', ROOT / 'scripts/vps/archive.py')
archive = importlib.util.module_from_spec(spec)
spec.loader.exec_module(archive)


class BackupValidation(unittest.TestCase):
    def make_backup(self, root, path='.env.vps', link=False):
        for name in archive.FILES:
            (root / name).write_bytes(b'dump')
        for name in ['config.tar.gz', 'media-objects.tar.gz']:
            with tarfile.open(root / name, 'w:gz') as stream:
                member = tarfile.TarInfo(path if name.startswith('config') else 'object')
                if link:
                    member.type = tarfile.SYMTYPE
                    member.linkname = '/etc/shadow'
                    stream.addfile(member)
                else:
                    member.size = 1
                    stream.addfile(member, io.BytesIO(b'x'))
        (root / 'SHA256SUMS').write_text(''.join(
            hashlib.sha256((root / n).read_bytes()).hexdigest() + '  ' + n + '\n' for n in sorted(archive.FILES)))
        (root / 'COMPLETE').write_text('1\n')

    def test_valid_backup(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_backup(root)
            archive.verify(root)

    def test_corruption_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_backup(root)
            (root / 'auth.dump').write_bytes(b'corrupted')
            with self.assertRaisesRegex(ValueError, 'Checksum mismatch'):
                archive.verify(root)

    def test_traversal_links_and_unexpected_config_rejected(self):
        for path, link in [('../etc/shadow', False), ('/etc/shadow', False), ('.env.vps', True), ('scripts/vps.sh', False)]:
            with self.subTest(path=path, link=link), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                self.make_backup(root, path, link)
                with self.assertRaises(ValueError):
                    archive.verify(root)


class BackupFailure(unittest.TestCase):
    def test_failed_dump_unpauses_services_and_never_marks_complete(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bindir = root / 'bin'
            bindir.mkdir()
            trace = root / 'trace'
            helpers = root / 'scripts/vps'
            helpers.mkdir(parents=True)
            for name in ('encrypted_backup.py', 'backup_config.py'):
                (helpers / name).write_bytes((ROOT / 'scripts/vps' / name).read_bytes())
            config = root / 'rclone.conf'
            config.write_text('[backup]\ntype = s3\nendpoint = https://backup.example.com\n')
            config.chmod(0o600)
            (root / '.env.vps').write_text(f'BACKUP_AGE_RECIPIENT=fake\nBACKUP_REMOTE=backup:vault\nBACKUP_RCLONE_CONFIG={config}\n')
            (bindir / 'rclone').write_text('#!/bin/sh\nexit 0\n')
            (bindir / 'age').write_text('#!/bin/sh\nif [ "$#" = 4 ]; then cat > "$4"; else cat; fi\n')
            (bindir / 'id').write_text('#!/bin/sh\necho 0\n')
            (bindir / 'docker').write_text("""#!/bin/sh
case "$*" in
    *'ps --status running'*) echo deadbeef;;
    'pause deadbeef'|'unpause deadbeef') echo "$*" >> "$TRACE";;
    *pg_dump*) exit 42;;
    *) exit 1;;
esac
""")
            for script in bindir.iterdir():
                script.chmod(0o700)
            env = dict(os.environ, VAULT_ROOT=directory, VAULT_BACKUP_ROOT=str(root / 'backups'),
                       TRACE=str(trace), PATH=str(bindir) + ':' + os.environ['PATH'])
            env.pop('VAULT_LOCK_HELD', None)
            result = subprocess.run(['sh', str(ROOT / 'scripts/vps/backup.sh')], env=env, capture_output=True)
            self.assertEqual(result.returncode, 42, result.stderr)
            self.assertEqual(trace.read_text().splitlines(), ['pause deadbeef', 'unpause deadbeef'])
            self.assertEqual(list((root / 'backups').glob('*/COMPLETE')), [])


class RestoreProtection(unittest.TestCase):
    def test_existing_volume_without_labels_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bindir = root / 'bin'
            bindir.mkdir()
            (bindir / 'id').write_text('#!/bin/sh\necho 0\n')
            (bindir / 'docker').write_text("""#!/bin/sh
case "$*" in
    'volume ls'*) exit 0;;
    'volume inspect vault-chat_pgdata') exit 0;;
    *) exit 42;;
esac
""")
            for script in bindir.iterdir():
                script.chmod(0o700)
            env = dict(os.environ, VAULT_ROOT=directory, VAULT_PROJECT='vault-chat',
                       PATH=str(bindir) + ':' + os.environ['PATH'])
            env.pop('VAULT_LOCK_HELD', None)
            result = subprocess.run(['sh', str(ROOT / 'scripts/vps/restore.sh'), directory],
                                    env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('already exists', result.stderr)
            self.assertFalse((root / '.env.vps').exists())


if __name__ == '__main__':
    unittest.main()
