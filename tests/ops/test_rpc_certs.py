"""Issuance and rotation must preserve the deployment CA and isolate private keys."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('rpc_certs', ROOT / 'scripts/gen-rpc-certs.py')
certs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(certs)


class Certificates(unittest.TestCase):
    def test_repeat_and_rotation_preserve_ca_and_isolate_service_keys(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            certs.issue(root)
            before = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob('*') if p.is_file()}
            certs.issue(root)
            self.assertEqual(before, {str(p.relative_to(root)): p.read_bytes() for p in root.rglob('*') if p.is_file()})
            self.assertEqual((root / 'rpc-ca/ca.key').stat().st_mode & 0o777, 0o600)
            for service in certs.SERVICES:
                leaf = root / 'rpc' / service
                self.assertEqual({p.name for p in leaf.iterdir()}, {'ca.pem', 'tls.crt', 'tls.key'})
                output = certs.openssl('x509', '-in', leaf / 'tls.crt', '-text', '-noout').decode()
                self.assertIn('URI:spiffe://vault-chat/service/' + service, output)
            certs.issue(root, rotate=True)
            self.assertEqual(before['rpc-ca/ca.key'], (root / 'rpc-ca/ca.key').read_bytes())
            self.assertEqual(before['rpc-ca/ca.pem'], (root / 'rpc-ca/ca.pem').read_bytes())
            self.assertNotEqual(before['rpc/auth/tls.key'], (root / 'rpc/auth/tls.key').read_bytes())
            certs.issue(root)  # Verify all rotated certs and key pairs.

    def test_incomplete_ca_is_not_replaced(self):
        with tempfile.TemporaryDirectory() as directory:
            ca = Path(directory) / 'rpc-ca'
            ca.mkdir()
            (ca / 'ca.key').write_text('existing signing key')
            with self.assertRaises(ValueError):
                certs.issue(directory)
            self.assertEqual((ca / 'ca.key').read_text(), 'existing signing key')
