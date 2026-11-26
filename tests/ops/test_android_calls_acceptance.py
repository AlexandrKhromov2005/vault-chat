import tempfile
import unittest
from pathlib import Path

from tests.ops.android_calls_acceptance import result


class Process:
    def __init__(self, code=0):
        self.code = code

    def wait(self, timeout):
        return self.code


class InstrumentationResultTest(unittest.TestCase):
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
