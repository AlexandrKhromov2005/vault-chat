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

class DowngradeProtection(unittest.TestCase):
    def test_update_rejects_legacy_image_before_changing_tag_or_backing_up(self):
        import os
        import subprocess
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / '.env.vps').write_text('VAULT_IMAGE_TAG=v0.4.0\n')
            (root / '.env.rpc').write_text('RPC_TLS_MODE=mtls\n')
            bindir = root / 'bin'
            bindir.mkdir()
            (bindir / 'id').write_text('#!/bin/sh\necho 0\n')
            (bindir / 'docker').write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$TRACE"\ncase "$*" in "image inspect "*) exit 0;; run*) exit 42;; *) exit 99;; esac\n')
            for p in bindir.iterdir():
                p.chmod(0o700)
            env = dict(os.environ, VAULT_ROOT=directory, TRACE=str(root / 'trace'), PATH=str(bindir) + ':' + os.environ['PATH'])
            env.pop('VAULT_LOCK_HELD', None)
            result = subprocess.run(['sh', str(ROOT / 'scripts/vps/update.sh'), 'v0.3.0'], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('-check-rpc-tls', (root / 'trace').read_text())
            self.assertNotIn('compose', (root / 'trace').read_text())
            self.assertEqual((root / '.env.vps').read_text(), 'VAULT_IMAGE_TAG=v0.4.0\n')
