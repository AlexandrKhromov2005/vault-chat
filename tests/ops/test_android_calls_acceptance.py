import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from tests.ops.android_calls_acceptance import instrumentation, result, wait_marker


class Process:
    def __init__(self, code=0):
        self.code = code

    def wait(self, timeout):
        return self.code

    def poll(self):
        return self.code


class InstrumentationResultTest(unittest.TestCase):
    @patch('tests.ops.android_calls_acceptance.subprocess.Popen')
    @patch('tests.ops.android_calls_acceptance.adb')
    def test_forwards_the_device_gateway(self, adb, popen):
        with tempfile.TemporaryDirectory() as folder:
            instrumentation('tablet', 'alice@example.test', 'startsAndroidAudioAndReleasesMicrophone',
                            Path(folder) / 'caller.log', gateway='http://127.0.0.1:8080')
        adb.assert_called_once_with('tablet', 'reverse', 'tcp:8080', 'tcp:8080')
        args = popen.call_args.args[0]
        index = args.index('gateway')
        self.assertEqual(args[index + 1], 'http://127.0.0.1:8080')

    def test_missing_marker_fails_when_instrumentation_already_exited(self):
        with self.assertRaisesRegex(AssertionError, 'instrumentation exited before calls-test-ready'):
            wait_marker('tablet', '/sdcard/calls-test-ready', 1, process=Process())

    def test_accepts_only_a_real_pass(self):
        with tempfile.TemporaryDirectory() as folder:
            log = Path(folder) / 'caller.log'
            log.write_text('INSTRUMENTATION_RESULT: stream=\nOK (1 test)\n')
            self.assertEqual(result(Process(), log, 1), 0)

    def test_process_crash_is_not_a_pass(self):
        with tempfile.TemporaryDirectory() as folder:
            log = Path(folder) / 'caller.log'
            log.write_text('INSTRUMENTATION_RESULT: shortMsg=Process crashed.\nINSTRUMENTATION_CODE: 0\n')
            with self.assertRaisesRegex(AssertionError, 'instrumentation failed'):
                result(Process(), log, 1)

    def test_junit_failure_is_not_hidden_by_zero_exit(self):
        with tempfile.TemporaryDirectory() as folder:
            log = Path(folder) / 'callee.log'
            log.write_text('Tests run: 1, Failures: 1\nFAILURES!!!\nINSTRUMENTATION_CODE: -1\n')
            with self.assertRaisesRegex(AssertionError, 'instrumentation failed'):
                result(Process(), log, 1)


if __name__ == '__main__':
    unittest.main()
