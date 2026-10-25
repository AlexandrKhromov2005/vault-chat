"""Security gates fail before expensive checks and never print secret values."""
import importlib.util
import os
from pathlib import Path
import unittest
import subprocess
import tempfile
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('dev_security', ROOT / 'scripts/dev/security.py')
security = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(security)


class Security(unittest.TestCase):
    def test_private_key_and_local_configuration_are_rejected(self):
        for name, data in [('.env.vps', b'PASSWORD=example'), ('.env.production', b'PASSWORD=example'), ('secrets/key', b'data'),
                           ('oops.pem', b'-----BEGIN RSA PRIVATE KEY-----\nYWJj\n-----END RSA PRIVATE KEY-----')]:
            self.assertTrue(security.secret_findings(name, data), name)

    def test_examples_and_generated_test_keys_are_not_false_positives(self):
        for name, data in [('.env.example', b'PASSWORD=fixture'), ('.env.vps.example', b'PASSWORD=fixture'),
                           ('key_test.go', b'pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})'),
                           ('README.md', b'Never commit private keys.')]:
            self.assertFalse(security.secret_findings(name, data), name)

    def test_known_credential_format_is_rejected(self):
        self.assertTrue(security.secret_findings('oops.txt', ('ghp_' + 'a'*36).encode()))
        identity = ('AGE-SECRET-' + 'KEY-1' + 'A'*58).encode()
        self.assertTrue(security.secret_findings('identity.txt', identity))

    def test_embedded_pem_literal_in_source_is_rejected(self):
        literal = b'var key = `-----BEGIN PRIVATE KEY-----\nYWJj\n-----END PRIVATE KEY-----`'
        self.assertTrue(security.secret_findings('key.go', literal))

    def test_node_and_tool_build_versions(self):
        security.check_node('v22.23.4')
        for old in ('v20.18.1', 'v22.16.0'):
            with self.assertRaisesRegex(ValueError, 'Node'):
                security.check_node(old)
        security.check_go_tool('lint', 'go1.27.3', '1.27.3')
        with self.assertRaisesRegex(ValueError, 'built'):
            security.check_go_tool('lint', 'go1.25.9', '1.27.3')

    def test_staged_secret_cannot_hide_behind_a_clean_working_file(self):
        with tempfile.TemporaryDirectory() as directory:
            subprocess.run(['git', 'init', '-q', directory], check=True)
            filename = Path(directory) / 'accident.txt'
            secret = 'ghp_' + 'z'*36
            filename.write_text(secret)
            subprocess.run(['git', '-C', directory, 'add', '.'], check=True)
            filename.write_text('now safe')
            with self.assertRaises(ValueError) as caught:
                security.scan(Path(directory), staged=True)
            self.assertIn('accident.txt', str(caught.exception))
            self.assertNotIn(secret, str(caught.exception))

    def test_build_network_cannot_attach_to_an_arbitrary_stack(self):
        with patch.dict(os.environ, {'VAULT_CI_BUILD_NETWORK': 'production-network'}):
            with self.assertRaisesRegex(ValueError, 'build network'):
                security.build_network()


if __name__ == '__main__':
    unittest.main()
