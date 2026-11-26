#!/usr/bin/env python3
import argparse
import json
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

ADB = 'adb'
APP = 'io.github.alexandrkhromov2005.vaultchat.debug'
RUNNER = APP + '.test/androidx.test.runner.AndroidJUnitRunner'
TEST = 'io.github.alexandrkhromov2005.vaultchat.core.calls.CallsInstrumentedTest'
MARKER = '/sdcard/Android/data/' + APP + '/files/calls-test-ready'
PASSWORD = 'SyntheticPassword123'


def command(*args, check=True, capture=False, cwd=None):
    return subprocess.run(args, check=check, text=True, stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
                          stderr=subprocess.PIPE if capture else subprocess.DEVNULL, timeout=360, cwd=cwd)


def adb(serial, *args, check=True):
    return command(ADB, '-s', serial, *args, check=check)


def api(base, method, path, *, token=None, body=None):
    headers = {}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    data = None
    if body is not None:
        headers['Content-Type'] = 'application/json'
        data = json.dumps(body).encode()
    request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def make_user(base, project, role, suffix):
    email = f'{role}{suffix}@example.test'
    username = f'{role}{suffix}'[:32]
    invitation = json.loads(command('docker', 'exec', f'{project}-auth-1', '/usr/local/bin/invite', email,
                                    check=True, capture=True).stdout)['invitation_token']
    api(base, 'POST', '/api/v1/auth/register', body={
        'email': email, 'username': username, 'password': PASSWORD, 'invitation_token': invitation,
    })
    pair = api(base, 'POST', '/api/v1/auth/login', body={'email': email, 'password': PASSWORD})
    return email, pair


def instrumentation(serial, email, method, log, room_id=None):
    args = [ADB, '-s', serial, 'shell', 'am', 'instrument', '-w', '-r',
            '-e', 'email', email, '-e', 'password', PASSWORD]
    if room_id:
        args += ['-e', 'roomId', room_id]
    args += ['-e', 'class', f'{TEST}#{method}', RUNNER]
    return subprocess.Popen(args, stdout=log.open('w'), stderr=subprocess.STDOUT)


def wait_ready(serial, timeout=240):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if adb(serial, 'shell', 'test', '-f', MARKER, check=False).returncode == 0:
            return
        time.sleep(2)
    raise TimeoutError(f'{serial}: instrumented login did not become ready')


def result(process, log, timeout):
    try:
        code = process.wait(timeout=timeout)
    finally:
        text = log.read_text()
        if 'OK (1 test)' not in text or 'FAILURES' in text:
            raise AssertionError(f'instrumentation failed; private log: {log}')
    return code


def main():
    parser = argparse.ArgumentParser(description='Android-to-Android WebRTC audio acceptance')
    parser.add_argument('--project', required=True)
    parser.add_argument('--gateway', default='http://127.0.0.1:8080')
    parser.add_argument('--caller', default='emulator-5554')
    parser.add_argument('--callee', default='emulator-5556')
    parser.add_argument('--build', action='store_true')
    args = parser.parse_args()
    if args.gateway not in ('http://127.0.0.1:8080', 'http://localhost:8080'):
        raise ValueError('acceptance requires the loopback gateway on port 8080')
    root = Path(__file__).resolve().parents[2]
    private = Path(tempfile.mkdtemp(prefix='vault-android-calls-'))
    caller_log = private / 'caller.log'
    callee_log = private / 'callee.log'
    caller = callee = None
    try:
        if args.build:
            command('./gradlew', '-Pvaultchat.callsForceRelay=true', ':app:assembleDebug', ':app:assembleAndroidTest',
                    cwd=root / 'android')
        debug = root / 'android/app/build/outputs/apk/debug/app-debug.apk'
        android_test = root / 'android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk'
        for serial in (args.caller, args.callee):
            adb(serial, 'install', '--no-streaming', '-r', '-g', str(debug))
            adb(serial, 'install', '--no-streaming', '-r', str(android_test))
        suffix = str(time.time_ns())
        alice, alice_pair = make_user(args.gateway, args.project, 'alice', suffix)
        bob, bob_pair = make_user(args.gateway, args.project, 'bob', suffix)
        room = api(args.gateway, 'POST', '/api/v1/rooms/direct', token=alice_pair['access_token'],
                   body={'peer_user_id': bob_pair['user_id']})
        adb(args.callee, 'shell', 'rm', '-f', MARKER)
        callee = instrumentation(args.callee, bob, 'acceptsBrowserAudioAndReleasesMicrophone', callee_log)
        wait_ready(args.callee)
        caller = instrumentation(args.caller, alice, 'startsAndroidAudioAndReleasesMicrophone', caller_log,
                                 room_id=room['room_id'])
        result(caller, caller_log, 1_000)
        result(callee, callee_log, 120)
        active = api(args.gateway, 'GET', '/api/v1/calls?active=true', token=alice_pair['access_token'])
        if active['calls']:
            raise AssertionError('server still has an active call after both clients ended')
        print(f'Android-to-Android audio acceptance passed; private logs: {private}')
    except Exception:
        for process in (caller, callee):
            if process is not None and process.poll() is None:
                process.terminate()
        for serial in (args.caller, args.callee):
            adb(serial, 'shell', 'am', 'force-stop', APP, check=False)
        print(f'Failed logs: {private}')
        raise


if __name__ == '__main__':
    main()
