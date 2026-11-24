#!/usr/bin/env python3
"""Check that release shrinking preserves WebRTC's JNI entry points."""
import argparse
import os
import subprocess


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('apk')
    parser.add_argument('--apkanalyzer', default=os.environ.get('APKANALYZER', 'apkanalyzer'))
    args = parser.parse_args()
    result = subprocess.run(
        [args.apkanalyzer, 'dex', 'packages', args.apk],
        check=True, capture_output=True, text=True,
    )
    lines = result.stdout.splitlines()

    def has_class(name):
        return any(line.startswith('C ') and line.split()[-1] == name for line in lines)

    required = ['org.webrtc.WebRtcClassLoader', 'org.webrtc.PeerConnectionFactory', 'org.jni_zero.JniZero']
    missing = [name for name in required if not has_class(name)]
    if missing:
        print('Missing WebRTC classes: ' + ', '.join(missing))
        return 1
    methods = {
        'org.webrtc.WebRtcClassLoader': 'getClassLoader(',
        'org.jni_zero.JniZero': 'setJniClassLoader(',
    }
    for name, method in methods.items():
        if not any(name + ' ' in line and method in line for line in lines):
            print('Missing native entry point: ' + name + '.' + method[:-1])
            return 1
    print('WebRTC JNI classes and native classloader entry point are retained')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
