# Cross-compile every release binary. Requires Go; produces artefacts that
# require nothing at all.
#
# Usage: .\scripts\build-release.ps1 [version]

param([string]$Version = 'dev')

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

$module  = 'github.com/Aryan27-max/bento-box'
$ldflags = "-s -w -X $module/internal/cli.Version=$Version"

if (Test-Path dist) { Remove-Item -Recurse -Force dist }
New-Item -ItemType Directory dist | Out-Null

# CGO_ENABLED=0 is what makes the output a static binary with no libc
# dependency, which is the whole promise of the release artefacts.
$env:CGO_ENABLED = '0'

$targets = @(
    @{ os = 'windows'; arch = 'amd64'; ext = '.exe' },
    @{ os = 'windows'; arch = 'arm64'; ext = '.exe' },
    @{ os = 'linux';   arch = 'amd64'; ext = '' },
    @{ os = 'linux';   arch = 'arm64'; ext = '' },
    @{ os = 'darwin';  arch = 'amd64'; ext = '' },
    @{ os = 'darwin';  arch = 'arm64'; ext = '' }
)

Write-Host "Building Bento Box $Version"
foreach ($target in $targets) {
    $output = "dist/bento-$($target.os)-$($target.arch)$($target.ext)"
    $env:GOOS = $target.os
    $env:GOARCH = $target.arch

    Write-Host ("  building {0,-24}" -f "$($target.os)/$($target.arch)") -NoNewline
    & go build -trimpath -ldflags $ldflags -o $output ./cmd/bento
    if ($LASTEXITCODE -ne 0) { throw "build failed for $($target.os)/$($target.arch)" }

    $size = [math]::Round((Get-Item $output).Length / 1MB, 1)
    Write-Host "ok  (${size} MB)"
}

Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED

Write-Host ''
Write-Host 'Checksums'
Get-ChildItem dist/bento-* | ForEach-Object {
    $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
    "$hash  $($_.Name)"
} | Tee-Object -FilePath dist/checksums.txt
