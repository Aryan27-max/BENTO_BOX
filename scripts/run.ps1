# Developer convenience launcher for Windows.
#
# This is NOT how end users run Bento. Released binaries are self-contained and
# need no Go installation; this script exists only so contributors can run the
# code they are editing. See startcmd.md.

$ErrorActionPreference = 'Stop'

Set-Location (Join-Path $PSScriptRoot '..')

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host @'
Go is not installed.

This launcher is for developing Bento, and building Bento from source needs Go.
If you only want to *use* Bento, download a release binary instead - it needs
no Go and no other runtime:

    https://github.com/Aryan27-max/bento-box/releases
'@
    exit 1
}

& go run ./cmd/bento @args
exit $LASTEXITCODE
