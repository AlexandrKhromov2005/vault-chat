"""Terminating verification must stop its checks and finish fixture cleanup."""
import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / 'scripts/dev/verification.py'
SPEC = importlib.util.spec_from_file_location('termination_verification', MODULE)
verification = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verification)

RUNNER = '''import importlib.util, pathlib, subprocess, sys, tempfile, time
spec = importlib.util.spec_from_file_location('verification', sys.argv[1])
verification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verification)
root, fixture = map(pathlib.Path, sys.argv[2:4])
repeat = sys.argv[4] == 'repeat'
verification.inventory = lambda _: {'tools': 'fixture', 'docker_endpoint': 'unix:///tmp/vault-fixture.sock'}
make_directory = tempfile.mkdtemp
verification.tempfile.mkdtemp = lambda **kwargs: make_directory(dir=fixture, **kwargs)
child = "import os,pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(60)"
def checks(snapshot, environment, log):
    try:
        return verification.run_process(
            [sys.executable, '-c', child, str(fixture / 'child.pid')],
            cwd=snapshot, env=environment, stdout=log, stderr=subprocess.STDOUT,
            timeout=30, check=True).returncode
    finally:
        (fixture / 'cleanup-started').write_text('cleanup started')
        deadline = time.monotonic() + 10
        while repeat and not (fixture / 'finish-cleanup').exists():
            if time.monotonic() >= deadline:
                raise TimeoutError('fixture cleanup was not released')
            time.sleep(0.01)
        (fixture / 'cleaned').write_text('fixture cleanup completed')
verification.execute_checks = checks
verification.verify(root)
'''


class Termination(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='vault-termination-test-')
        self.addCleanup(self.temporary.cleanup)
        self.fixture = Path(self.temporary.name)
        self.root = self.fixture / 'repository'
        self.root.mkdir(mode=0o700)
        self.git('init', '-q', '-b', 'feature/termination-test')
        self.git('config', 'user.name', 'Test')
        self.git('config', 'user.email', 'test@example.invalid')
        (self.root / 'source').write_text('synthetic fixture\n')
        self.git('add', 'source')
        self.git('commit', '-qm', 'test: synthetic source')

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], text=True).strip()

    def test_sigterm_stops_child_and_runs_cleanup_without_a_receipt(self):
        self.run_sigterm_case()

    def test_repeated_sigterm_does_not_interrupt_cleanup(self):
        self.run_sigterm_case(repeat=True)

    def run_sigterm_case(self, repeat=False):
        pidfile = self.fixture / 'child.pid'
        child_pid = None
        with (self.fixture / 'runner.log').open('w') as log:
            runner = subprocess.Popen(
                [sys.executable, '-c', RUNNER, str(MODULE), str(self.root), str(self.fixture),
                 'repeat' if repeat else 'once'],
                stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline and runner.poll() is None:
                    try:
                        child_pid = int(pidfile.read_text())
                        break
                    except (FileNotFoundError, ValueError):
                        time.sleep(0.02)
                self.assertIsNotNone(child_pid, 'verification did not start the synthetic check')
                runner.send_signal(signal.SIGTERM)
                if repeat:
                    cleanup_started = self.fixture / 'cleanup-started'
                    deadline = time.monotonic() + 10
                    while (not cleanup_started.exists() and runner.poll() is None
                           and time.monotonic() < deadline):
                        time.sleep(0.02)
                    self.assertTrue(cleanup_started.is_file(), 'verification skipped cleanup')
                    runner.send_signal(signal.SIGTERM)
                    with self.assertRaises(subprocess.TimeoutExpired, msg='second SIGTERM interrupted cleanup'):
                        runner.wait(timeout=0.1)
                    (self.fixture / 'finish-cleanup').write_text('release cleanup')
                self.assertNotEqual(runner.wait(timeout=10), 0)
                try:
                    os.kill(child_pid, 0)
                    child_running = True
                except ProcessLookupError:
                    child_running = False
                self.assertEqual(
                    {'child_running': child_running,
                     'cleanup_ran': (self.fixture / 'cleaned').is_file(),
                     'receipt_written': bool(list((self.root / '.git/vault-verification').glob('*.json')))},
                    {'child_running': False, 'cleanup_ran': True, 'receipt_written': False})
            finally:
                if runner.poll() is None:
                    runner.kill()
                    runner.wait(timeout=5)
                if child_pid is not None:
                    try:
                        os.killpg(child_pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass

    def test_previous_sigterm_handler_is_restored_on_success_and_failure(self):
        previous = signal.getsignal(signal.SIGTERM)
        def handler(*_):
            pass
        signal.signal(signal.SIGTERM, handler)
        try:
            with patch.object(verification, 'verify_locked', return_value='verified'):
                self.assertEqual(verification.verify(self.root), 'verified')
            self.assertIs(signal.getsignal(signal.SIGTERM), handler)
            with patch.object(verification, 'verify_locked', side_effect=ValueError('fixture failed')):
                with self.assertRaisesRegex(ValueError, 'fixture failed'):
                    verification.verify(self.root)
            self.assertIs(signal.getsignal(signal.SIGTERM), handler)
        finally:
            signal.signal(signal.SIGTERM, previous)


if __name__ == '__main__':
    unittest.main()
