"""Fixture builds must use the pinned daemon despite a selected Buildx builder."""
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('builder_verification', ROOT / 'scripts/dev/verification.py')
verification = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verification)


class BuilderSelection(unittest.TestCase):
    def test_fixture_build_ignores_ambient_global_buildx_builder(self):
        # This is the on-disk Buildx current/instances format. Both sockets are
        # synthetic paths; all Docker processes below are replaced by fakes.
        with tempfile.TemporaryDirectory(prefix='vault-builder-test-') as directory:
            root = Path(directory)
            config = root / 'docker-config'
            instances = config / 'buildx' / 'instances'
            instances.mkdir(parents=True, mode=0o700)
            (config / 'config.json').write_text('{}')
            (config / 'buildx' / 'current').write_text(json.dumps({
                'Key': 'unused', 'Name': 'ambient-builder', 'Global': True,
            }))
            (instances / 'ambient-builder').write_text(json.dumps({
                'Name': 'ambient-builder', 'Driver': 'remote',
                'Nodes': [{'Name': 'ambient-builder0', 'Endpoint': 'unix://' + str(root / 'other.sock')}],
            }))
            pinned = 'unix://' + str(root / 'pinned.sock')
            environment = {'DOCKER_HOST': pinned, 'DOCKER_CONFIG': str(config)}
            endpoints = []
            builders = []

            def capture(args, **kwargs):
                # Namespace discovery finds no existing resources; port lookup
                # returns a disposable synthetic endpoint without contacting it.
                return '127.0.0.1:12345' if 'port' in args else ''

            def run(args, **kwargs):
                if args[:2] == ['docker', 'compose'] and 'build' in args:
                    env = kwargs['env']
                    builder = args[args.index('--builder') + 1] if '--builder' in args else None
                    builders.append(builder)
                    if builder == 'default':
                        endpoint = env['DOCKER_HOST']
                    else:
                        # Compose/Buildx otherwise honors a globally selected
                        # instance, independently of Docker's DOCKER_HOST.
                        buildx = Path(env['DOCKER_CONFIG']) / 'buildx'
                        current = json.loads((buildx / 'current').read_text())
                        self.assertTrue(current['Global'])
                        selected = json.loads((buildx / 'instances' / current['Name']).read_text())
                        endpoint = selected['Nodes'][0]['Endpoint']
                    endpoints.append(endpoint)
                return subprocess.CompletedProcess(args, 0)

            with patch.object(verification.subprocess, 'check_output', side_effect=capture), \
                    patch.object(verification, 'run_process', side_effect=run):
                result = verification.execute_checks(root, environment, io.StringIO())

            self.assertEqual(result, 0)
            self.assertEqual(endpoints, [pinned], 'fixture build escaped the pinned Docker daemon')
            self.assertEqual(builders, ['default'], 'fixture build must explicitly select the default builder')


if __name__ == '__main__':
    unittest.main()
