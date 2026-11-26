#!/bin/sh
set -eu

: "${ANDROID_HOME:?set ANDROID_HOME}"
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
export ANDROID_AVD_HOME=${ANDROID_AVD_HOME:-"$ROOT/.tools/android-avd-acceptance"}
AVD_MANAGER="$ANDROID_HOME/cmdline-tools/latest/bin/avdmanager"
EMULATOR="$ANDROID_HOME/emulator/emulator"
IMAGE='system-images;android-26;default;x86_64'
mkdir -p "$ANDROID_AVD_HOME"

for serial in emulator-5554 emulator-5556; do
    if "$ANDROID_HOME/platform-tools/adb" -s "$serial" get-state >/dev/null 2>&1; then
        printf '%s is already running; stop it before creating a fresh acceptance device\n' "$serial" >&2
        exit 1
    fi
done
for name in vault-call-a vault-call-b; do
    if [ ! -f "$ANDROID_AVD_HOME/$name.ini" ]; then
        printf 'no\n' | "$AVD_MANAGER" create avd -n "$name" -k "$IMAGE" -d pixel_2
    fi
done

nohup "$EMULATOR" -avd vault-call-a -port 5554 -accel off -memory 1536 -cores 2 \
    -skin 540x960 -gpu swiftshader_indirect -no-window -no-boot-anim -no-snapshot -wipe-data -no-metrics \
    >"$ANDROID_AVD_HOME/caller.log" 2>&1 &
printf 'caller pid %s\n' "$!"
nohup "$EMULATOR" -avd vault-call-b -port 5556 -accel off -memory 1536 -cores 2 \
    -skin 540x960 -gpu swiftshader_indirect -no-window -no-boot-anim -no-snapshot -wipe-data -no-metrics \
    >"$ANDROID_AVD_HOME/callee.log" 2>&1 &
printf 'callee pid %s\n' "$!"
