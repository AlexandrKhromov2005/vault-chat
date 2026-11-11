"""Security gates fail before expensive checks and never print secret values."""
import importlib.util
import os
from pathlib import Path
import re
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

    def test_erlang_toolchain_is_pinned_exactly(self):
        security.check_otp('28.5.0.8')
        for other in ('28.5.0.7', '28.5', '28.5.0.80', '27.3.4.2', ''):
            with self.assertRaisesRegex(ValueError, 'Erlang/OTP'):
                security.check_otp(other)
        security.check_rebar3('rebar 3.27.3 on Erlang/OTP 28 Erts 16.4.0.7')
        for other in ('rebar 3.27.2 on Erlang/OTP 28 Erts 16.4.0.7', 'rebar 3.27.30 on Erlang/OTP 28 Erts 16.4.0.7',
                      'rebar3 3.27.3', ''):
            with self.assertRaisesRegex(ValueError, 'rebar3'):
                security.check_rebar3(other)

    def test_ci_runs_the_calls_checks(self):
        # A commit that breaks Calls must not receive green evidence.
        steps = re.search(r'local steps = \{(.*?)\n        \}', (ROOT / 'xmake.lua').read_text(), re.S).group(1)
        self.assertIn('{"calls"}', steps)

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


class VulnerabilityDatabase(unittest.TestCase):
    def test_local_snapshot_is_fingerprinted_and_remote_override_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            entry = Path(directory) / 'GO-2026-0001.json'
            entry.write_text('{"id":"GO-2026-0001"}')
            url = Path(directory).as_uri()
            with patch.dict(os.environ, {'GOVULNDB': url}):
                first = security.vulndb()
                self.assertEqual(first['url'], url)
                entry.write_text('{"id":"GO-2026-0001","changed":true}')
                self.assertNotEqual(first['sha256'], security.vulndb()['sha256'])
            with patch.dict(os.environ, {'GOVULNDB': 'https://mirror.invalid'}):
                with self.assertRaisesRegex(ValueError, 'official|file'):
                    security.vulndb()


class VulnerabilityCommand(unittest.TestCase):
    def test_xmake_forwards_local_database_as_an_explicit_flag(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'govulncheck'
            arguments = Path(directory) / 'arguments'
            binary.write_text(f'#!/bin/sh\nprintf "%s\\n" "$@" > {arguments}\n')
            binary.chmod(0o755)
            environment = dict(os.environ, PATH=directory + os.pathsep + os.environ['PATH'], GOVULNDB='file:///tmp/official-vulndb')
            subprocess.run(['xmake', 'vuln', '-P', '.'], cwd=ROOT, env=environment, check=True)
            self.assertEqual(arguments.read_text().splitlines(), ['-db', 'file:///tmp/official-vulndb', './...'])
