"""VPS installation must set an explicit least-privilege firewall policy."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class FirewallPolicy(unittest.TestCase):
    def test_install_sets_default_policies_before_enabling_ufw(self):
        with tempfile.TemporaryDirectory(prefix='vault-firewall-ops-') as directory:
            root = Path(directory)
            trace = root / 'trace'
            bindir = root / 'bin'
            bindir.mkdir(mode=0o700)
            ufw = bindir / 'ufw'
            ufw.write_text('''#!/usr/bin/env python3
import json,os,sys
with open(os.environ['TRACE'],'a') as stream:
    stream.write(json.dumps(sys.argv[1:])+'\\n')
''')
            ufw.chmod(0o700)
            installer = (ROOT / 'scripts/vps/install.sh').read_text()
            commands = [line for line in installer.splitlines() if line.strip().startswith('ufw ')]
            script = root / 'firewall.sh'
            script.write_text('set -eu\n' + '\n'.join(commands) + '\n')
            environment = dict(os.environ, PATH=str(bindir) + os.pathsep + os.defpath, TRACE=str(trace))
            result = subprocess.run(['sh', str(script)], env=environment,
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            calls = [json.loads(line) for line in trace.read_text().splitlines()]
            self.assertEqual(calls, [
                ['default', 'deny', 'incoming'],
                ['default', 'allow', 'outgoing'],
                ['default', 'deny', 'routed'],
                ['allow', 'OpenSSH'],
                ['allow', '80/tcp'],
                ['allow', '443/tcp'],
                ['--force', 'enable'],
            ])


if __name__ == '__main__':
    unittest.main()
