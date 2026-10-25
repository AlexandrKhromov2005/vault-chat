"""CI must keep the validated local Docker endpoint without contacting a daemon."""
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('docker_verification', ROOT / 'scripts/dev/verification.py')
verification = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verification)
security = importlib.import_module('security')


@unittest.skipUnless(shutil.which('docker'), 'Docker CLI required for isolated context fixtures')
class DockerEnvironment(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='vault-docker-environment-')
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.home = self.directory / 'home'
        self.home.mkdir(mode=0o700)
        self.default_config = self.home / '.docker'
        self.selected_config = self.directory / 'selected-docker'
        self.default_config.mkdir(mode=0o700)
        self.selected_config.mkdir(mode=0o700)
        self.environment = {'PATH': os.environ['PATH'], 'HOME': str(self.home), 'LANG': 'C', 'USER': 'fixture'}
        self.local_endpoint = 'unix://' + str(self.directory / 'synthetic-local.sock')
        self.remote_endpoint = 'tcp://192.0.2.1:2375'
        self.context(self.default_config, 'fixture-remote', self.remote_endpoint)
        self.context(self.selected_config, 'fixture-local', self.local_endpoint)
        self.context(self.selected_config, 'fixture-remote', self.remote_endpoint)
        self.select(self.default_config, 'fixture-remote')
        self.select(self.selected_config, 'fixture-local')
        self.environment['DOCKER_CONFIG'] = str(self.selected_config)

    def command(self, args, environment=None):
        # Context create/use/inspect only read or write these private JSON fixtures.
        # None of these commands open a Docker socket or contact a remote daemon.
        return subprocess.check_output(args, env=environment or self.environment,
                                       text=True, stderr=subprocess.PIPE).strip()

    def context(self, config, name, endpoint):
        self.command(['docker', '--config', str(config), 'context', 'create', name,
                      '--docker', 'host=' + endpoint])

    def select(self, config, name):
        self.command(['docker', '--config', str(config), 'context', 'use', name])

    def endpoint(self, environment):
        return self.command(['docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], environment)

    def verify_environment(self, action=None):
        root = self.directory / 'repository'
        root.mkdir(mode=0o700)
        self.command(['git', '-C', str(root), 'init', '-q', '-b', 'fixture'])
        self.command(['git', '-C', str(root), 'config', 'user.name', 'Synthetic Fixture'])
        self.command(['git', '-C', str(root), 'config', 'user.email', 'fixture@example.invalid'])
        (root / 'source').write_text('synthetic source\n')
        self.command(['git', '-C', str(root), 'add', 'source'])
        self.command(['git', '-C', str(root), 'commit', '-qm', 'test: synthetic source'])
        selected = []

        def inspect_environment(_snapshot, environment, _log):
            if action:
                action()
            selected.append(self.endpoint(environment))
            return 0

        make_directory = tempfile.mkdtemp
        with patch.dict(os.environ, self.environment, clear=True), \
                patch.object(verification, 'inventory', return_value={'docker_endpoint': self.local_endpoint}), \
                patch.object(verification.tempfile, 'mkdtemp',
                             side_effect=lambda **kwargs: make_directory(dir=self.directory, **kwargs)), \
                patch.object(verification, 'execute_checks', side_effect=inspect_environment):
            verification.verify(root)
        self.assertEqual(selected, [self.local_endpoint])

    def test_validated_custom_config_cannot_fall_back_to_remote_default(self):
        self.assertEqual(self.endpoint(self.environment), self.local_endpoint)
        self.verify_environment()

    def test_context_change_after_validation_cannot_retarget_checks(self):
        self.assertEqual(self.endpoint(self.environment), self.local_endpoint)
        self.verify_environment(lambda: self.select(self.selected_config, 'fixture-remote'))

    def test_filtered_environment_pins_endpoint_and_preserves_config(self):
        with patch.dict(os.environ, dict(self.environment, DOCKER_CONTEXT='fixture-local'), clear=True):
            environment = verification.check_environment({'docker_endpoint': self.local_endpoint})
        self.assertEqual(environment['DOCKER_HOST'], self.local_endpoint)
        self.assertEqual(environment['DOCKER_CONFIG'], str(self.selected_config))
        self.assertNotIn('DOCKER_CONTEXT', environment)
        self.assertEqual(self.endpoint(environment), self.local_endpoint)

    def test_relative_config_remains_valid_in_a_different_checkout(self):
        original_directory = Path.cwd()
        with patch.dict(os.environ, dict(self.environment, DOCKER_CONFIG='selected-docker'), clear=True):
            try:
                os.chdir(self.directory)
                environment = verification.check_environment({'docker_endpoint': self.local_endpoint})
            finally:
                os.chdir(original_directory)
        self.assertEqual(environment['DOCKER_CONFIG'], str(self.selected_config))
        self.assertEqual(self.endpoint(environment), self.local_endpoint)

    def test_inventory_without_validated_endpoint_fails_closed(self):
        with patch.dict(os.environ, self.environment, clear=True):
            with self.assertRaises(ValueError):
                verification.check_environment({'tools': 'synthetic'})

    def test_remote_current_context_is_rejected(self):
        environment = dict(self.environment, DOCKER_CONFIG=str(self.default_config))
        with self.assertRaises(ValueError):
            security.docker_endpoint(environment)

    def test_remote_and_relative_socket_hosts_are_rejected(self):
        for endpoint in (self.remote_endpoint, 'ssh://fixture@example.invalid', 'unix://relative.sock'):
            with self.subTest(endpoint=endpoint), self.assertRaises(ValueError):
                security.docker_endpoint(dict(self.environment, DOCKER_HOST=endpoint))

    def test_cli_normalized_local_socket_is_accepted(self):
        environment = dict(self.environment, DOCKER_HOST='unix://')
        effective = self.endpoint(environment)
        self.assertTrue(effective.startswith('unix:///'))
        self.assertEqual(security.docker_endpoint(environment), effective)

    def test_endpoint_selection_matches_cli_with_both_context_and_host(self):
        for context, host in [('fixture-local', self.remote_endpoint), ('fixture-remote', self.local_endpoint)]:
            with self.subTest(context=context, host=host):
                environment = dict(self.environment, DOCKER_CONTEXT=context, DOCKER_HOST=host)
                effective = self.endpoint(environment)
                if effective == self.local_endpoint:
                    self.assertEqual(security.docker_endpoint(environment), effective)
                else:
                    self.assertEqual(effective, self.remote_endpoint)
                    with self.assertRaises(ValueError):
                        security.docker_endpoint(environment)


if __name__ == '__main__':
    unittest.main()
