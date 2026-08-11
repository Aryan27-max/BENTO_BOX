#!/usr/bin/env bash
#
# Developer convenience launcher for Linux and macOS.
#
# This is NOT how end users run Bento. Released binaries are self-contained and
# need no Go installation; this script exists only so contributors can run the
# code they are editing. See startcmd.md.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if ! command -v go >/dev/null 2>&1; then
    cat >&2 <<'EOF'
Go is not installed.

This launcher is for developing Bento, and building Bento from source needs Go.
If you only want to *use* Bento, download a release binary instead — it needs
no Go and no other runtime:

    https://github.com/Aryan27-max/bento-box/releases
EOF
    exit 1
fi

exec go run ./cmd/bento "$@"
