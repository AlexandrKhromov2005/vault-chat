"""VPS initialization must preserve live credentials and keys."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class Initialization(unittest.TestCase):
    def test_repeat_preserves_credentials_and_keys(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'scripts').mkdir()
            (root / 'scripts/gen-jwt-keys.sh').write_bytes((ROOT / 'scripts/gen-jwt-keys.sh').read_bytes())
            command = ['python3', str(ROOT / 'scripts/vps/init.py'), '168.113.171.56', 'v0.2.0']
            env = dict(os.environ, VAULT_ROOT=directory)
            subprocess.run(command, env=env, check=True, capture_output=True)
            before = {name: (root / name).read_bytes() for name in ['.env.vps', 'secrets/auth_jwt_private.pem']}
            subprocess.run(command, env=env, check=True, capture_output=True)
            self.assertEqual(before, {name: (root / name).read_bytes() for name in before})
            self.assertEqual((root / '.env.vps').stat().st_mode & 0o777, 0o600)
            result = subprocess.run(command[:-1] + ['different-tag'], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(before['.env.vps'], (root / '.env.vps').read_bytes())


if __name__ == '__main__':
    unittest.main()
