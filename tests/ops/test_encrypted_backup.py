"""Encryption must preserve data and propagate producer failures."""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


@unittest.skipUnless(shutil.which('age') and shutil.which('age-keygen'), 'age tools required')
class Encryption(unittest.TestCase):
    def test_round_trip_and_failed_producer(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            identity = root / 'identity'
            subprocess.run(['age-keygen', '-o', str(identity)], check=True, capture_output=True)
            recipient = subprocess.check_output(['age-keygen', '-y', str(identity)], text=True).strip()
            cipher = root / 'config.age'
            plain = b'secret configuration and private keys\n'
            command = [sys.executable, str(ROOT / 'scripts/vps/encrypted_backup.py'), recipient, str(cipher),
                       sys.executable, '-c', f'import sys;sys.stdout.buffer.write({plain!r})']
            subprocess.run(command, check=True)
            self.assertNotIn(plain, cipher.read_bytes())
            result = subprocess.check_output(['age', '-d', '-i', str(identity), str(cipher)])
            self.assertEqual(result, plain)
            command[3] = str(root / 'failed.age')
            command[-1] += ';sys.exit(42)'
            failed = subprocess.run(command, capture_output=True)
            self.assertEqual(failed.returncode, 42, failed.stderr)
            self.assertFalse((root / 'failed.age').exists())
