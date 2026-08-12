# startcmd.md — how to run Bento Box

This file is the single source of truth for running, testing and building this
project. If a command changes, it changes here. [README.md](README.md) is the
project overview; this is the detailed command reference it links to.

There are two audiences below. Please do not confuse them.

- **End users** run a released binary. They need nothing installed — no Go, no
  Python, no Node, no runtime of any kind. Their instructions are
  [section 3](#3-end-users).
- **Developers** work on Bento itself. They need Go and Git. Their
  instructions are [section 2](#2-developer-setup).

Every section below is split by operating system wherever the commands differ.
Find your OS, start at Step 1, and run the steps in order.

**Shell conventions used throughout this document.** Every code block is
labelled with the shell it belongs to:

- Blocks marked **powershell** are **PowerShell**, on **Windows**.
- Blocks marked **bash** are **bash** on **Linux**, or **bash or zsh** on
  **macOS** (zsh is the macOS default and accepts every command shown here).

Environment-variable syntax differs between PowerShell and bash, so commands are
never shared between the two in this document. Each is written out in full for
the shell it belongs to.

---

## Table of contents

1. [Prerequisites](#1-prerequisites)
2. [Developer setup](#2-developer-setup)
3. [End users](#3-end-users)
4. [Testing](#4-testing)
5. [Release builds](#5-release-builds)
6. [Releasing](#6-releasing)
7. [Where Bento writes](#7-where-bento-writes)

---

## 1. Prerequisites

Only developers need these. End users need nothing — skip to
[section 3](#3-end-users).

| Prerequisite | Version | Why |
| --- | --- | --- |
| Go | **1.25 or newer** | Bento is written in Go; `go.mod` declares `go 1.25.0` |
| Git | any recent version | To clone the repository |

Nothing else is required. There is no Makefile to install, no Node toolchain,
no Python.

### Windows

Bento does not assume you already have Go or Git.

**Go** — download the Windows installer (`.msi`) from
[go.dev/dl](https://go.dev/dl/) and run it. Pick `windows-amd64` on an Intel or
AMD machine, `windows-arm64` on Windows on ARM.

**Git** — download the installer from
[git-scm.com/download/win](https://git-scm.com/download/win) and run it.

If you already have **winget**, both of the above are equivalent to:

```powershell
winget install --id GoLang.Go --source winget
winget install --id Git.Git --source winget
```

Either way, **close PowerShell and open a new window afterwards**. Installers
modify PATH, and an already-open shell will not see the change.

### Linux

**Go** — distribution packages are frequently older than 1.25, so use the
official tarball. Check [go.dev/dl](https://go.dev/dl/) for the current version
and substitute it for `go1.25.0` below. On an ARM64 machine, use the
`linux-arm64` tarball instead of `linux-amd64`.

```bash
curl -LO https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

That `export` lasts only for the current shell. Add it to `~/.bashrc` (or
`~/.zshrc`) to make it permanent:

```bash
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
```

**Git** — from your distribution's package manager. Bento supports several, and
so does this instruction:

| Distribution | Command |
| --- | --- |
| Debian, Ubuntu | `sudo apt update && sudo apt install -y git` |
| Fedora, RHEL, CentOS | `sudo dnf install -y git` |
| Arch, Manjaro | `sudo pacman -S --needed git` |
| openSUSE | `sudo zypper install -y git` |

If your distribution's Go package is already 1.25 or newer, installing Go from
the package manager is fine too. Verify with `go version` before relying on it.

### macOS

**Go** — download the macOS package (`.pkg`) from
[go.dev/dl](https://go.dev/dl/) and run it. Pick `darwin-arm64` on Apple
Silicon (M1 and later), `darwin-amd64` on Intel.

If you already have **Homebrew**, this is equivalent:

```bash
brew install go
```

**Git** — ships with the Xcode Command Line Tools. If `git --version` fails or
prompts you to install them:

```bash
xcode-select --install
```

---

## 2. Developer setup

Find your operating system, then run every step in order. Each step assumes the
previous one succeeded.

---

### Windows

All commands in this subsection are **PowerShell**.

#### Step 1 — Install Go and Git

See [Prerequisites → Windows](#windows). Open a **new** PowerShell window
afterwards.

#### Step 2 — Verify Go

```powershell
go version
```

Expected — the patch number will differ, and anything **1.25 or newer** is fine:

```text
go version go1.25.0 windows/amd64
```

If this reports `go: command not found` or `is not recognized`, Go is not
installed or not on PATH. Go back to Step 1 and open a new terminal.

Verify Git as well:

```powershell
git --version
```

#### Step 3 — Clone Bento

```powershell
git clone https://github.com/Aryan27-max/bento-box.git
```

#### Step 4 — Enter the repository

```powershell
cd bento-box
```

Every remaining command in this subsection runs from inside this directory.

#### Step 5 — Build Bento

```powershell
go build ./...
```

Success is silent. This compiles every package but produces no binary in the
working directory. To produce an executable you can run directly:

```powershell
go build -o bento.exe ./cmd/bento
```

#### Step 6 — Run the tests

```powershell
go test ./...
```

Every package should report `ok`. These tests touch nothing on your real
system — see [section 4](#4-testing).

#### Step 7 — Run Bento from source

```powershell
go run ./cmd/bento --dry-run
```

`--dry-run` shows exactly what Bento would do and changes nothing. Other useful
invocations while developing:

```powershell
go run ./cmd/bento --profile web --plan
go run ./cmd/bento --list
go run ./cmd/bento --help
```

If you built an executable in Step 5, run it instead:

```powershell
.\bento.exe --dry-run
```

There is also a convenience launcher that wraps `go run`:

```powershell
.\scripts\run.ps1 --dry-run
```

> `scripts/run.ps1` is a developer convenience. It is **not** the product. The
> released binary never invokes a shell script, and the core logic lives
> entirely in Go.

#### Step 8 — Formatting

```powershell
gofmt -l .
```

This lists files that need formatting and prints nothing when everything is
clean. **CI fails if it prints anything.** To fix them in place:

```powershell
gofmt -l -w .
```

#### Step 9 — Static analysis

```powershell
go vet ./...
```

Success is silent. Optionally, if you have it installed:

```powershell
staticcheck ./...
```

Or without installing it:

```powershell
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

#### Step 10 — Race detector

```powershell
go test -race ./...
```

CI runs this on all three operating systems, so run it before opening a pull
request.

---

### Linux

All commands in this subsection are **bash**.

#### Step 1 — Install Go and Git

See [Prerequisites → Linux](#linux).

#### Step 2 — Verify Go

```bash
go version
```

Expected — the patch number will differ, and anything **1.25 or newer** is fine:

```text
go version go1.25.0 linux/amd64
```

If this reports `go: command not found`, Go is not installed or
`/usr/local/go/bin` is not on your PATH. Go back to Step 1.

Verify Git as well:

```bash
git --version
```

#### Step 3 — Clone Bento

```bash
git clone https://github.com/Aryan27-max/bento-box.git
```

#### Step 4 — Enter the repository

```bash
cd bento-box
```

Every remaining command in this subsection runs from inside this directory.

#### Step 5 — Build Bento

```bash
go build ./...
```

Success is silent. This compiles every package but produces no binary in the
working directory. To produce an executable you can run directly:

```bash
go build -o bento ./cmd/bento
```

#### Step 6 — Run the tests

```bash
go test ./...
```

Every package should report `ok`. These tests touch nothing on your real
system — see [section 4](#4-testing).

#### Step 7 — Run Bento from source

```bash
go run ./cmd/bento --dry-run
```

`--dry-run` shows exactly what Bento would do and changes nothing. Other useful
invocations while developing:

```bash
go run ./cmd/bento --profile web --plan
go run ./cmd/bento --list
go run ./cmd/bento --help
```

If you built an executable in Step 5, run it instead:

```bash
./bento --dry-run
```

There is also a convenience launcher that wraps `go run`:

```bash
./scripts/run.sh --dry-run
```

> `scripts/run.sh` is a developer convenience. It is **not** the product. The
> released binary never invokes a shell script, and the core logic lives
> entirely in Go.

#### Step 8 — Formatting

```bash
gofmt -l .
```

This lists files that need formatting and prints nothing when everything is
clean. **CI fails if it prints anything.** To fix them in place:

```bash
gofmt -l -w .
```

#### Step 9 — Static analysis

```bash
go vet ./...
```

Success is silent. Optionally, if you have it installed:

```bash
staticcheck ./...
```

Or without installing it:

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

#### Step 10 — Race detector

```bash
go test -race ./...
```

CI runs this on all three operating systems, so run it before opening a pull
request.

---

### macOS

All commands in this subsection are **zsh** (the macOS default) or **bash**.

#### Step 1 — Install Go and Git

See [Prerequisites → macOS](#macos).

#### Step 2 — Verify Go

```bash
go version
```

Expected — the patch number will differ, and anything **1.25 or newer** is fine:

```text
go version go1.25.0 darwin/arm64
```

On an Intel Mac the last field reads `darwin/amd64` instead. If this reports
`go: command not found`, Go is not installed or not on your PATH. Go back to
Step 1.

Verify Git as well:

```bash
git --version
```

#### Step 3 — Clone Bento

```bash
git clone https://github.com/Aryan27-max/bento-box.git
```

#### Step 4 — Enter the repository

```bash
cd bento-box
```

Every remaining command in this subsection runs from inside this directory.

#### Step 5 — Build Bento

```bash
go build ./...
```

Success is silent. This compiles every package but produces no binary in the
working directory. To produce an executable you can run directly:

```bash
go build -o bento ./cmd/bento
```

#### Step 6 — Run the tests

```bash
go test ./...
```

Every package should report `ok`. These tests touch nothing on your real
system — see [section 4](#4-testing).

#### Step 7 — Run Bento from source

```bash
go run ./cmd/bento --dry-run
```

`--dry-run` shows exactly what Bento would do and changes nothing. Other useful
invocations while developing:

```bash
go run ./cmd/bento --profile web --plan
go run ./cmd/bento --list
go run ./cmd/bento --help
```

If you built an executable in Step 5, run it instead:

```bash
./bento --dry-run
```

There is also a convenience launcher that wraps `go run`:

```bash
./scripts/run.sh --dry-run
```

> `scripts/run.sh` is a developer convenience. It is **not** the product. The
> released binary never invokes a shell script, and the core logic lives
> entirely in Go.

> **Do not confuse these two commands.** `go run ./cmd/bento` is the developer
> workflow: it compiles the source in this repository and runs it, and it
> requires Go. `./bento-darwin-arm64` is the released binary an end user
> downloads from the Releases page, and it requires nothing at all. They are
> documented separately for that reason — the end-user form is in
> [section 3](#3-end-users).

#### Step 8 — Formatting

```bash
gofmt -l .
```

This lists files that need formatting and prints nothing when everything is
clean. **CI fails if it prints anything.** To fix them in place:

```bash
gofmt -l -w .
```

#### Step 9 — Static analysis

```bash
go vet ./...
```

Success is silent. Optionally, if you have it installed:

```bash
staticcheck ./...
```

Or without installing it:

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

#### Step 10 — Race detector

```bash
go test -race ./...
```

CI runs this on all three operating systems, so run it before opening a pull
request.

---

### Developer command summary

The same commands apply on every platform once Go is installed. This table is a
reminder for people who have already followed the ordered steps above; it is not
a replacement for them.

| Task | Windows (PowerShell) | Linux (bash) | macOS (zsh/bash) |
| --- | --- | --- | --- |
| Verify Go | `go version` | `go version` | `go version` |
| Clone | `git clone https://github.com/Aryan27-max/bento-box.git` | `git clone https://github.com/Aryan27-max/bento-box.git` | `git clone https://github.com/Aryan27-max/bento-box.git` |
| Enter repo | `cd bento-box` | `cd bento-box` | `cd bento-box` |
| Build all packages | `go build ./...` | `go build ./...` | `go build ./...` |
| Build an executable | `go build -o bento.exe ./cmd/bento` | `go build -o bento ./cmd/bento` | `go build -o bento ./cmd/bento` |
| Test | `go test ./...` | `go test ./...` | `go test ./...` |
| Race detector | `go test -race ./...` | `go test -race ./...` | `go test -race ./...` |
| Coverage | `go test -cover ./...` | `go test -cover ./...` | `go test -cover ./...` |
| Static analysis | `go vet ./...` | `go vet ./...` | `go vet ./...` |
| Formatting check | `gofmt -l .` | `gofmt -l .` | `gofmt -l .` |
| Run from source | `go run ./cmd/bento --dry-run` | `go run ./cmd/bento --dry-run` | `go run ./cmd/bento --dry-run` |
| Convenience launcher | `.\scripts\run.ps1 --dry-run` | `./scripts/run.sh --dry-run` | `./scripts/run.sh --dry-run` |

Note the one genuine difference: the output filename when building an
executable is `bento.exe` on Windows and `bento` on Linux and macOS.

---

## 3. End users

Bento releases are self-contained binaries. **No Go, Python, Node or anything
else is required.** Go is a build-time dependency for developers only.

Download the binary for your platform from the
[Releases page](https://github.com/Aryan27-max/bento-box/releases):

| Platform | Architecture | Binary |
| --- | --- | --- |
| Windows | x64 (Intel, AMD) | `bento-windows-amd64.exe` |
| Windows | ARM64 (Windows on ARM) | `bento-windows-arm64.exe` |
| Linux | x64 | `bento-linux-amd64` |
| Linux | ARM64 | `bento-linux-arm64` |
| macOS | Intel | `bento-darwin-amd64` |
| macOS | Apple Silicon (M1 and later) | `bento-darwin-arm64` |

Pick the binary matching your architecture. The examples below use the most
common one for each platform; substitute the ARM64 name if that is your machine.

### Windows

PowerShell, from the folder you downloaded to. Nothing needs to be made
executable on Windows:

```powershell
.\bento-windows-amd64.exe
```

On a Windows on ARM machine, use the ARM64 binary instead:

```powershell
.\bento-windows-arm64.exe
```

### Linux

```bash
chmod +x ./bento-linux-amd64
./bento-linux-amd64
```

On an ARM64 machine, use `./bento-linux-arm64` in both commands instead.

### macOS

macOS quarantines binaries downloaded from the internet. Clear the attribute
once, then run it:

```bash
chmod +x ./bento-darwin-arm64
xattr -d com.apple.quarantine ./bento-darwin-arm64
./bento-darwin-arm64
```

On an Intel Mac, use `./bento-darwin-amd64` in all three commands instead.

### Verifying your download

Each release ships a `checksums.txt`. Download it alongside the binary and
verify before running.

**Windows** (PowerShell) — compare the printed hash against the matching line
in `checksums.txt`:

```powershell
Get-FileHash .\bento-windows-amd64.exe -Algorithm SHA256
```

**Linux**:

```bash
sha256sum -c checksums.txt --ignore-missing
```

**macOS**:

```bash
shasum -a 256 -c checksums.txt --ignore-missing
```

### Common invocations

These are identical on all three platforms; only the way you name the binary
differs. Substitute your platform's filename — `.\bento-windows-amd64.exe`,
`./bento-linux-amd64` or `./bento-darwin-arm64` — for `bento` below.

```text
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

A bare profile name also works, so `bento web` is equivalent to
`bento --profile web`. It must come **after** any flags, because flag parsing
stops at the first non-flag argument:

```text
bento web                # works
bento --plan web         # works — flags first
bento web --plan         # does NOT work; exits 2 with "unexpected arguments"
```

Use `--profile` if you want to be sure of the ordering.

### Profiles

| Profile | Accepted names |
| --- | --- |
| AI / ML | `ai`, `ai-ml`, `aiml`, `ml`, `machine-learning` |
| Web | `web`, `frontend`, `backend`, `fullstack` |
| Blockchain | `blockchain`, `web3`, `chain`, `evm` |
| App | `app`, `mobile`, `android` |
| NUKE | `nuke`, `all`, `everything` |

Every profile also inherits the core set. What each profile installs is in the
[README](README.md#profiles).

### Flags

| Flag | Effect |
| --- | --- |
| `-p, --profile <name>` | Profile to install (`ai`, `web`, `blockchain`, `app`, `nuke`) |
| `-y, --yes` | Skip the confirmation prompt |
| `--dry-run` | Plan and report without touching the machine |
| `--plan` | Print the plan and exit |
| `--json` | Write the report as JSON to stdout |
| `--verbose` | Show every installation step |
| `--no-color` | Disable colour |
| `-v, --version` | Print the version |
| `-h, --help` | Show the help text |

Exit codes: `0` success · `1` a dependency failed · `2` usage error ·
`130` you cancelled.

---

## 4. Testing

All test commands are identical on Windows, Linux and macOS. Run them from the
repository root.

### Unit tests

```bash
go test ./...
```

Unit tests never install software, never write to the registry and never call
the network. Package managers, command execution and environment writes are
behind interfaces with mocks.

To run a single package verbosely:

```bash
go test -v ./internal/resolver/
```

### Race detector

```bash
go test -race ./...
```

This is what CI runs, on all three operating systems.

### Coverage

```bash
go test -cover ./...
```

### Integration tests

These are behind a build tag, so they are **not** part of `go test ./...`. They
call the real vendor metadata endpoints (go.dev, nodejs.org,
storage.googleapis.com) to catch a vendor changing their release format. They
are read-only and install nothing.

```bash
go test -tags integration -v -run TestLive ./internal/installer/...
```

### What CI runs

`.github/workflows/ci.yml` runs on Ubuntu, Windows and macOS:

```bash
go mod download
go mod verify
go build ./...
go vet ./...
go test -race ./...
```

Plus, on Ubuntu only, the formatting gate (`gofmt -l .` must print nothing),
catalog validation, and a cross-compile of all six release targets.

---

## 5. Release builds

Every release target is a static binary with no runtime dependencies.
`CGO_ENABLED=0` guarantees that; `-trimpath` keeps local paths out of the
binary; `-s -w` strips debug information.

There are six targets, and they never change:

```text
Windows amd64      Linux amd64      macOS amd64
Windows arm64      Linux arm64      macOS arm64
```

Cross-compiling needs no extra toolchain — a single Go installation can build
all six from any of the three operating systems.

### All six at once (recommended)

**Windows** — PowerShell:

```powershell
.\scripts\build-release.ps1 0.1.0
```

**Linux and macOS** — bash:

```bash
./scripts/build-release.sh 0.1.0
```

Both scripts wipe and recreate `dist/`, build all six binaries, and write
`dist/checksums.txt`. The version argument is optional and defaults to `dev`.

### One target at a time — Linux and macOS (bash)

Environment variables are set inline, as a prefix to the command. Set the
version once per shell session:

```bash
VERSION=0.1.0
LDFLAGS="-s -w -X github.com/Aryan27-max/bento-box/internal/cli.Version=$VERSION"
```

Then build whichever targets you need:

```bash
# Windows amd64
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-windows-amd64.exe ./cmd/bento

# Windows arm64
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-windows-arm64.exe ./cmd/bento

# Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-linux-amd64 ./cmd/bento

# Linux arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-linux-arm64 ./cmd/bento

# macOS amd64
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-darwin-amd64 ./cmd/bento

# macOS arm64
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/bento-darwin-arm64 ./cmd/bento
```

### One target at a time — Windows (PowerShell)

**PowerShell does not accept the `VAR=value command` prefix syntax used above.**
Environment variables are assigned separately through `$env:`, they persist for
the rest of the session, and they must be removed afterwards or they will
silently affect every later `go build` in that window.

```powershell
$env:CGO_ENABLED = '0'
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'

go build -trimpath -ldflags "-s -w -X github.com/Aryan27-max/bento-box/internal/cli.Version=0.1.0" -o dist/bento-linux-amd64 ./cmd/bento

Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

Change `$env:GOOS` and `$env:GOARCH` for the other five targets, using these
values and output names:

| Target | `$env:GOOS` | `$env:GOARCH` | Output |
| --- | --- | --- | --- |
| Windows amd64 | `windows` | `amd64` | `dist/bento-windows-amd64.exe` |
| Windows arm64 | `windows` | `arm64` | `dist/bento-windows-arm64.exe` |
| Linux amd64 | `linux` | `amd64` | `dist/bento-linux-amd64` |
| Linux arm64 | `linux` | `arm64` | `dist/bento-linux-arm64` |
| macOS amd64 | `darwin` | `amd64` | `dist/bento-darwin-amd64` |
| macOS arm64 | `darwin` | `arm64` | `dist/bento-darwin-arm64` |

In practice, use `.\scripts\build-release.ps1` instead — it handles all six and
cleans up the environment variables for you.

---

## 6. Releasing

Releases are built by GitHub Actions, which is the only place Go is needed to
produce the artefacts users download.

Before tagging, run the same gates CI runs.

**Windows** — PowerShell. `&&` is not available in Windows PowerShell, so run
these as separate commands and check each one:

```powershell
go test ./...
gofmt -l .
go vet ./...
.\scripts\build-release.ps1 0.1.0
```

**Linux and macOS** — bash:

```bash
go test ./... && gofmt -l . && go vet ./... && ./scripts/build-release.sh 0.1.0
```

`gofmt -l .` must print nothing. Then tag and push — this works identically on
all three platforms:

```bash
git tag v0.1.0
git push origin v0.1.0
```

`.github/workflows/release.yml` then tests on Ubuntu, Windows and macOS,
cross-compiles all six targets, verifies the Linux binary is statically linked,
generates checksums, and attaches everything to the GitHub release.

The workflow can also be triggered manually from the Actions tab, which takes
the version as an input instead of reading it from the tag.

---

## 7. Where Bento writes

Useful when developing, and the first place to look when debugging. Bento's own
state lives under `~/.bento` — on Windows that is `%USERPROFILE%\.bento`, which
PowerShell writes as `$HOME\.bento`.

| Path | Contents |
| --- | --- |
| `~/.bento/report.json` | The most recent run's report |
| `~/.bento/logs/bento-<timestamp>.log` | Every command, exit status and duration |
| `~/.bento/environment.json` | Bento's record of the variables and PATH entries it manages |
| `~/.bento/opt/` | Toolchains unpacked from official archives |
| `~/.bento/downloads/` | Transient; cleaned up after each install |

### Resetting Bento's own state

This removes Bento's records without touching any software it installed.

**Windows** — PowerShell:

```powershell
Remove-Item -Recurse -Force $HOME\.bento
```

**Linux and macOS** — bash:

```bash
rm -rf ~/.bento
```

### Undoing environment changes

Resetting the state directory does **not** undo environment changes. Those have
to be reverted where they were written.

**Linux and macOS** — remove the block between the `# >>> bento box >>>` and
`# <<< bento box <<<` markers in your shell configuration file (`.bashrc`,
`.zshrc`, `config.fish` or `.profile`). Everything outside the markers is yours
and was never touched.

**Windows** — edit the variables under `HKCU\Environment`, either with the
Environment Variables dialog (search for "environment variables" in the Start
menu) or with PowerShell:

```powershell
Get-ItemProperty -Path HKCU:\Environment
```

Bento never writes to `HKLM` and never uses `setx`, which truncates a long PATH.
