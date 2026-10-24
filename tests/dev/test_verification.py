"""A green result must refer to the committed source actually tested."""
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('verification', ROOT / 'scripts/dev/verification.py')
verification = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verification)


class Verification(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.logs = tempfile.TemporaryDirectory()
        self.addCleanup(self.logs.cleanup)
        self.root = Path(self.temporary.name)
        self.git('init', '-q', '-b', 'feature/test')
        self.git('config', 'user.name', 'Test')
        self.git('config', 'user.email', 'test@example.invalid')
        (self.root / 'source').write_text('original\n')
        self.commit()

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], text=True).strip()

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'test: source')

    def run_checks(self, action=None, code=0):
        make_directory = tempfile.mkdtemp
        def check(snapshot, environment, log):
            self.assertEqual((snapshot / 'source').read_text(), 'original\n')
            self.assertFalse((snapshot / '.env').exists())
            self.assertNotIn('GH_TOKEN', environment)
            if action:
                action(snapshot)
            return code
        with patch.dict(os.environ, {'GH_TOKEN': 'do-not-copy'}), \
                patch.object(verification, 'inventory', return_value={'tools': 'pinned'}), \
                patch.object(verification.tempfile, 'mkdtemp', side_effect=lambda **kw: make_directory(dir=self.logs.name, **kw)), \
                patch.object(verification, 'execute_checks', side_effect=check):
            return verification.verify(self.root)

    def test_snapshot_does_not_include_working_secrets(self):
        (self.root / '.gitignore').write_text('.env\n')
        self.commit()
        (self.root / '.env').write_text('private configuration')
        receipt = self.run_checks()
        self.assertEqual(receipt['commit'], self.git('rev-parse', 'HEAD'))
        verification.require_receipt(self.root, receipt['commit'], {'tools': 'pinned'})

    def test_source_change_during_checks_cannot_receive_a_green_receipt(self):
        def edit(_):
            (self.root / 'source').write_text('untested\n')
        with self.assertRaisesRegex(ValueError, 'changed|dirty'):
            self.run_checks(edit)
        self.assertFalse(list((self.root / '.git/vault-verification').glob('*.json')))

    def test_snapshot_change_also_invalidates_the_result(self):
        def edit(snapshot):
            (snapshot / 'source').write_text('changed by a check')
        with self.assertRaisesRegex(ValueError, 'snapshot|dirty'):
            self.run_checks(edit)

    def test_failed_rerun_removes_an_old_success(self):
        receipt = self.run_checks()
        with self.assertRaisesRegex(ValueError, 'failed'):
            self.run_checks(code=1)
        with self.assertRaisesRegex(ValueError, 'verification'):
            verification.require_receipt(self.root, receipt['commit'], {'tools': 'pinned'})

    def test_another_commit_or_tool_change_is_not_covered(self):
        receipt = self.run_checks()
        with self.assertRaisesRegex(ValueError, 'tools'):
            verification.require_receipt(self.root, receipt['commit'], {'tools': 'changed'})
        (self.root / 'source').write_text('next\n')
        self.commit()
        with self.assertRaisesRegex(ValueError, 'verification'):
            verification.require_receipt(self.root, self.git('rev-parse', 'HEAD'), {'tools': 'pinned'})

    def test_expired_receipts_are_rejected(self):
        receipt = self.run_checks()
        receipt['completed_at'] = 0
        verification.receipt_path(self.root, receipt['commit']).write_text(json.dumps(receipt))
        with self.assertRaisesRegex(ValueError, 'expired'):
            verification.require_receipt(self.root, receipt['commit'], {'tools': 'pinned'})

    def test_changed_log_cannot_be_published_as_evidence(self):
        receipt = self.run_checks()
        Path(receipt['log']).write_text('replaced evidence')
        with self.assertRaisesRegex(ValueError, 'log'):
            verification.require_receipt(self.root, receipt['commit'], {'tools': 'pinned'})

    def test_dirty_checkout_is_rejected_before_checks(self):
        (self.root / 'source').write_text('uncommitted')
        with patch.object(verification, 'execute_checks') as check:
            with self.assertRaisesRegex(ValueError, 'dirty'):
                verification.verify(self.root)
            check.assert_not_called()

    def test_parallel_checks_cannot_overwrite_each_others_result(self):
        def second_run(_):
            with self.assertRaisesRegex(ValueError, 'already running'):
                verification.verify(self.root)
        self.run_checks(second_run)

    def test_push_requires_receipts_for_every_pushed_tip(self):
        receipt = self.run_checks()
        sha = receipt['commit']
        line = f'refs/heads/test {sha} refs/heads/test {"0"*40}\n'
        verification.verify_push(self.root, [line], {'tools': 'pinned'})
        with self.assertRaisesRegex(ValueError, 'tools'):
            verification.verify_push(self.root, [line], {'tools': 'changed'})
        verification.verify_push(self.root, [f'delete {"0"*40} refs/heads/test {sha}\n'], {})

    def test_merge_requires_current_head_and_independent_approval(self):
        base = {'state': 'OPEN', 'isDraft': False, 'reviewDecision': 'APPROVED', 'headRefOid': 'a'*40}
        verification.merge_policy(base, 'a'*40)
        for change in ({'reviewDecision': ''}, {'isDraft': True}, {'headRefOid': 'b'*40}):
            with self.assertRaises(ValueError):
                verification.merge_policy(dict(base, **change), 'a'*40)

    def test_environment_drops_production_and_external_test_endpoints(self):
        with patch.dict(os.environ, {'AWS_SECRET_ACCESS_KEY': 'secret', 'AUTH_DATABASE_URL': 'production',
                                   'VAULT_CHAT_TEST_DATABASE_URL': 'external', 'GOFLAGS': '-unsafe'}):
            env = verification.check_environment()
        for name in ('AWS_SECRET_ACCESS_KEY', 'AUTH_DATABASE_URL', 'VAULT_CHAT_TEST_DATABASE_URL', 'GOFLAGS'):
            self.assertNotIn(name, env)

    def test_existing_docker_namespace_is_never_started_or_cleaned(self):
        with patch.object(verification.subprocess, 'check_output', return_value='existing'), \
                patch.object(verification, 'run_process') as mutate:
            with self.assertRaisesRegex(ValueError, 'already exists'):
                verification.execute_checks(self.root, {}, io.StringIO())
            mutate.assert_not_called()

    def test_partial_fixture_start_failure_still_cleans_only_owned_project(self):
        calls = []
        def run(args, **kwargs):
            calls.append(args)
            if 'up' in args:
                raise subprocess.CalledProcessError(1, args)
        with patch.object(verification.subprocess, 'check_output', return_value=''), \
                patch.object(verification, 'run_process', side_effect=run):
            with self.assertRaises(subprocess.CalledProcessError):
                verification.execute_checks(self.root, {}, io.StringIO())
        self.assertEqual(len(calls), 3)
        self.assertIn('down', calls[-1])
        self.assertEqual(calls[0][3], calls[1][3])
        self.assertTrue(calls[1][3].startswith('vault-ci-'))

    def test_deadline_stops_descendants_before_fixture_cleanup(self):
        pidfile = Path(self.logs.name) / 'child.pid'
        program = '''import pathlib, signal, subprocess, sys, time
child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])
def stop(*_):
    child.wait(timeout=5)
    sys.exit(0)
signal.signal(signal.SIGTERM, stop)
pathlib.Path(sys.argv[1]).write_text(str(child.pid))
time.sleep(60)
'''
        with self.assertRaises(subprocess.TimeoutExpired):
            verification.run_process([sys.executable, '-c', program, str(pidfile)], timeout=1)
        with self.assertRaises(ProcessLookupError):
            os.kill(int(pidfile.read_text()), 0)

    def test_one_shot_provisioner_is_not_waited_on_as_a_running_service(self):
        def capture(args, **kwargs):
            return '127.0.0.1:12345' if 'port' in args else ''
        with patch.object(verification.subprocess, 'check_output', side_effect=capture), \
                patch.object(verification, 'run_process', return_value=subprocess.CompletedProcess([], 0)) as run:
            self.assertEqual(verification.execute_checks(self.root, {}, io.StringIO()), 0)
        commands = [call.args[0] for call in run.call_args_list]
        up = next(args for args in commands if 'up' in args)
        self.assertEqual(up[-2:], ['postgres', 'minio'])
        self.assertTrue(any(args[-4:] == ['run', '--rm', '--no-deps', 's3-init'] for args in commands))


if __name__ == '__main__':
    unittest.main()
