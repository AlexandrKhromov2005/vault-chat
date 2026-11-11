#!/bin/sh
set -eu

case "${GOVULNDB:-https://vuln.go.dev}" in
  https://vuln.go.dev)
    exec govulncheck "$@"
    ;;
  file://*)
    exec govulncheck -db "$GOVULNDB" "$@"
    ;;
  *)
    echo "GOVULNDB must be the official URL or a local file:// snapshot" >&2
    exit 1
    ;;
esac
