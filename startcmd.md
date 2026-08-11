# startcmd.md — how to run Bento Box

This file is the single source of truth for running, testing and building this
project. If a command changes, it changes here.

There are two audiences below. Please do not confuse them.

- **End users** run a released binary. They need nothing installed.
- **Developers** work on Bento itself. They need Go.

---

## 1. End users

Bento releases are self-contained binaries. No Go, Python, Node or anything
else is required.

### Windows

```powershell
# Download bento-windows-amd64.exe (or -arm64) from the Releases page, then:
.\bento-windows-amd64.exe
```

### Linux

```bash
chmod +x ./bento-linux-amd64
./bento-linux-amd64
```

### macOS

```bash
chmod +x ./bento-darwin-arm64
# macOS quarantines downloaded binaries; clear the attribute once:
xattr -d com.apple.quarantine ./bento-darwin-arm64
./bento-darwin-arm64
```

### Common invocations

```bash
bento                            # interactive profile menu
bento --profile web              # named profile
bento --profile nuke --dry-run   # show everything that would happen, change nothing
bento --profile ai --yes         # unattended
bento --plan                     # print the plan and stop
bento --json > report.json       # machine-readable report
bento --list                     # list profiles
bento --version
bento --help
```

---

## 2. Developer setup

Requires **Go 1.25 or newer**. Check with `go version`.

### Clone

```bash
git clone https://github.com/Aryan27-max/bento-box.git
cd bento-box
```

### Build

```bash
go build ./...                       # compile everything
go build -o bento ./cmd/bento        # Linux, macOS
go build -o bento.exe ./cmd/bento    # Windows
```

### Run from source

```bash
go run ./cmd/bento --dry-run
go run ./cmd/bento --profile web --plan
```

Or use the convenience launchers, which just wrap `go run`:

```bash
./scripts/run.sh --dry-run              # Linux, macOS
```

```powershell
.\scripts\run.ps1 --dry-run             # Windows
```

> These scripts are developer conveniences. They are **not** the product. The
> released binary never invokes a shell script, and the core logic lives
> entirely in Go.

---

## 3. Testing

```bash
go test ./...                     # full unit suite; touches no real system
go test -race ./...               # with the race detector
go test -cover ./...              # with coverage
go test -v ./internal/resolver/   # one package, verbose
```

Unit tests never install software, never write to the registry and never call
the network. Package managers, command execution and environment writes are
behind interfaces with mocks.

### Integration tests

These call the real vendor metadata endpoints (go.dev, nodejs.org,
storage.googleapis.com) to catch a vendor changing their release format. They
are read-only and install nothing.

```bash
go test -tags integration -v -run TestLive ./internal/installer/...
```

---

## 4. Formatting

```bash
gofmt -l .          # list files that need formatting
gofmt -l -w .       # format in place
```

CI fails if `gofmt -l .` prints anything.

---

## 5. Static analysis

```bash
go vet ./...
```

Optional, if installed:

```bash
staticcheck ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

---

## 6. Release builds

Every release target is a static binary with no runtime dependencies.
`CGO_ENABLED=0` guarantees that; `-trimpath` keeps local paths out of the
binary; `-s -w` strips debug information.

Set the version once:

```bash
VERSION=0.1.0
LDFLAGS="-s -w -X github.com/Aryan27-max/bento-box/internal/cli.Version=$VERSION"
```

### Windows amd64

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-windows-amd64.exe ./cmd/bento
```

### Windows arm64

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-windows-arm64.exe ./cmd/bento
```

### Linux amd64

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-linux-amd64 ./cmd/bento
```

### Linux arm64

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-linux-arm64 ./cmd/bento
```

### macOS amd64

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-darwin-amd64 ./cmd/bento
```

### macOS arm64

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-darwin-arm64 ./cmd/bento
```

### All six at once

```bash
./scripts/build-release.sh 0.1.0
```

```powershell
.\scripts\build-release.ps1 0.1.0
```

Both scripts also write `dist/checksums.txt`.

### On Windows using PowerShell directly

PowerShell sets environment variables differently:

```powershell
$env:CGO_ENABLED=0; $env:GOOS="linux"; $env:GOARCH="amd64"
go build -trimpath -ldflags "-s -w -X github.com/Aryan27-max/bento-box/internal/cli.Version=0.1.0" -o dist/bento-linux-amd64 ./cmd/bento
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

---

## 7. Releasing

Releases are built by GitHub Actions, which is the only place Go is needed to
produce the artefacts users download.

```bash
git tag v0.1.0
git push origin v0.1.0
```

`.github/workflows/release.yml` then tests, cross-compiles all six targets,
generates checksums and attaches everything to the GitHub release.

To check the workflow locally before tagging:

```bash
go test ./... && gofmt -l . && go vet ./... && ./scripts/build-release.sh 0.1.0
```

---

## 8. Where Bento writes

Useful when developing, and the first place to look when debugging.

| Path | Contents |
| --- | --- |
| `~/.bento/report.json` | The most recent run's report |
| `~/.bento/logs/bento-<timestamp>.log` | Every command, exit status and duration |
| `~/.bento/environment.json` | Bento's record of the variables and PATH entries it manages |
| `~/.bento/opt/` | Toolchains unpacked from official archives |
| `~/.bento/downloads/` | Transient; cleaned up after each install |

To reset Bento's own state without touching installed software:

```bash
rm -rf ~/.bento          # Linux, macOS
```

```powershell
Remove-Item -Recurse -Force $HOME\.bento    # Windows
```

Note that this does not undo environment changes. On Unix, remove the block
between the `# >>> bento box >>>` and `# <<< bento box <<<` markers in your
shell configuration file. On Windows, edit the variables under
`HKCU\Environment` (or use the Environment Variables dialog).
