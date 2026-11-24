from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
IMAGE = 'coturn/coturn:4.19.2-alpine3.24@sha256:974824bdefa8759b7aa4508daf5066fdd9f5411e925055b6132fb2904aecca01'


class TurnEntrypoint(unittest.TestCase):
    def test_public_to_private_mapping_is_never_zero(self):
        for private in (None, '10.20.30.40'):
            with self.subTest(private=private), tempfile.TemporaryDirectory(prefix='vault-turn-config-') as directory:
                fixture = Path(directory)
                secret = fixture / 'secret'
                secret.write_text('synthetic-turn-secret')
                secret.chmod(0o644)
                probe = fixture / 'turnserver'
                probe.write_text("#!/bin/sh\nsed -n 's/^external-ip=/external-ip=/p' /tmp/turnserver.conf\n")
                probe.chmod(0o755)
                environment = ['-e', 'TURN_PUBLIC_IP=192.0.2.10']
                if private is not None:
                    environment += ['-e', f'TURN_PRIVATE_IP={private}']
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
                self.assertEqual(result.stdout.strip(), f'external-ip=192.0.2.10/{private or "192.0.2.10"}')


if __name__ == '__main__':
    unittest.main()
