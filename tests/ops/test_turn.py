from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
IMAGE = 'coturn/coturn:4.19.2-alpine3.24@sha256:974824bdefa8759b7aa4508daf5066fdd9f5411e925055b6132fb2904aecca01'


class TurnEntrypoint(unittest.TestCase):
    def render(self, private=None, relay=None, verbose=False):
        with tempfile.TemporaryDirectory(prefix='vault-turn-config-') as directory:
            fixture = Path(directory)
            secret = fixture / 'secret'
            secret.write_text('synthetic-turn-secret')
            secret.chmod(0o644)
            probe = fixture / 'turnserver'
            probe.write_text("#!/bin/sh\nsed -n -e 's/^external-ip=/external-ip=/p' -e 's/^relay-ip=/relay-ip=/p' -e '/^verbose$/p' /tmp/turnserver.conf\n")
            probe.chmod(0o755)
            environment = ['-e', 'TURN_PUBLIC_IP=192.0.2.10']
            if private is not None:
                environment += ['-e', f'TURN_PRIVATE_IP={private}']
            if relay is not None:
                environment += ['-e', f'TURN_RELAY_IP={relay}']
            if verbose:
                environment += ['-e', 'TURN_VERBOSE=true']
            command = [
                'docker', 'run', '--rm', '--network', 'none', '--user', '65532:65532', '--read-only',
                '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--tmpfs', '/tmp',
                *environment,
                '-v', f'{secret}:/run/secrets/turn_secret:ro',
                '-v', f'{ROOT / "deployments/turnserver-entrypoint.sh"}:/usr/local/bin/turn-entrypoint:ro',
                '-v', f'{probe}:/usr/local/bin/turnserver:ro',
                '--entrypoint', '/usr/local/bin/turn-entrypoint', IMAGE,
            ]
            result = subprocess.run(command, capture_output=True, text=True, timeout=60, check=True)
            return result.stdout.strip().splitlines()

    def test_public_to_private_mapping_is_never_zero(self):
        for private in (None, '10.20.30.40'):
            with self.subTest(private=private):
                self.assertEqual(self.render(private), [f'external-ip=192.0.2.10/{private or "192.0.2.10"}'])

    def test_relay_ip_is_opt_in(self):
        self.assertEqual(self.render(relay='127.0.0.1'), [
            'relay-ip=127.0.0.1',
            'external-ip=192.0.2.10/192.0.2.10',
        ])

    def test_verbose_logging_is_opt_in(self):
        self.assertEqual(self.render(verbose=True), [
            'external-ip=192.0.2.10/192.0.2.10',
            'verbose',
        ])


if __name__ == '__main__':
    unittest.main()
