#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
zig=${ZIG_BIN:-"$root/.tools/zig/0.17.1/zig"}
if [ ! -x "$zig" ]; then
    echo "Zig 0.17.1 not found at $zig; set ZIG_BIN" >&2
    exit 1
fi
[ "$("$zig" version)" = 0.17.1 ] || {
    echo "Zig 0.17.1 is required" >&2
    exit 1
}

cd "$root/tools/preview/zig"
"$zig" build -Doptimize=ReleaseFast
cd "$root"
go build -o "$root/.tools/preview-go-decoder" ./tools/preview/go-decoder
export PREVIEW_GO_WORKER="$root/.tools/preview-go-decoder"
export PREVIEW_ZIG_WORKER="$root/tools/preview/zig/zig-out/bin/preview-decoder"

case ${1:-test} in
    test)
        cd "$root/tools/preview/zig"
        "$zig" build test -Doptimize=ReleaseSafe
        cd "$root"
        go test -timeout 180s ./internal/media/preview ./tools/preview/go-decoder
        ;;
    memory)
        PREVIEW_PROCESS_MEMORY=1 go test -run '^TestProcessMemoryBudget$' -v -timeout 240s ./internal/media/preview
        ;;
    benchmark)
        PREVIEW_PROCESS_BASELINE=1 go test -run '^TestProcessBaselineReport$' -v -timeout 120m ./internal/media/preview
        ;;
    *)
        echo 'Usage: tools/preview/run.sh {test|memory|benchmark}' >&2
        exit 2
        ;;
esac
