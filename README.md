# 🍱 Bento Box

**One setup. Everything you need to start building.**

Bento Box turns a fresh Windows, Linux or macOS machine into a developer-ready
environment. You pick what you are building; Bento works out what that needs,
shows you the plan, installs it, configures your environment, verifies every
piece actually works, and tells you exactly what happened.

```
🍱  BENTO BOX
Developer Environment Bootstrapper 0.1.0

Detected system
  OS:                  Windows 11
  Architecture:        amd64
  Package managers:    winget
  Shell:               powershell

What are you building?

❯ 🤖  AI / ML       Python, notebooks, PyTorch, TensorFlow, Ollama
  🌐  Web           Node.js, TypeScript, Bun, Postgres, Mongo, Redis
  ⛓️  Blockchain    Foundry, Solidity, Go and Rust toolchains
  📱  App           Flutter, Android SDK, JDK, Kotlin, Gradle
  ☢️  NUKE — I WANT IT ALL
```

---

## You do not need Go to run Bento

Bento is written in Go, but Go is a **build-time** dependency only. Released
binaries are self-contained: one file, statically linked, no runtime, no
interpreter, no toolchain.

Download the binary for your platform, make it executable, run it:

| Platform | Binary |
| --- | --- |
| Windows | `bento-windows-amd64.exe`, `bento-windows-arm64.exe` |
| Linux | `bento-linux-amd64`, `bento-linux-arm64` |
| macOS | `bento-darwin-amd64`, `bento-darwin-arm64` |

```powershell
# Windows
.\bento.exe
```

```bash
# Linux and macOS
chmod +x ./bento
./bento
```

`go run` and `go build` are developer workflows. They are not how anyone is
meant to use the product.

---

## Why it exists

Setting up a machine is a day of work that everybody does slightly differently
and nobody enjoys. The existing options are a personal dotfiles repo that only
works on its author's machine, a wiki page that went stale two years ago, or a
shell script that assumes Ubuntu.

Bento takes the position that this should be one binary, that it should tell
you what it is going to do before it does it, and that it should never claim
something worked when it did not.

---

## What it does

```
Detect OS, architecture, distribution, package manager, shell, privileges, GPU
                              ↓
                     You choose a profile
                              ↓
     Resolve the dependency graph, deduplicate, order by prerequisite
                              ↓
        Check what is already installed and what version it is
                              ↓
                 Show the plan  →  you confirm
                              ↓
            Install · configure environment · verify · report
```

### Profiles

| Profile | What you get |
| --- | --- |
| 🤖 **AI / ML** | Python, pip, uv, JupyterLab, NumPy, pandas, scikit-learn, PyTorch, TensorFlow, Ollama, Postgres, MongoDB, Redis, CUDA *(only if an NVIDIA GPU is present)* |
| 🌐 **Web** | Node.js, npm, pnpm, Bun, TypeScript, Python, uv, Postgres, MySQL, MongoDB, Redis, Compass |
| ⛓️ **Blockchain** | Foundry (forge, cast, anvil), Solidity, Go, Rust, Node.js, TypeScript, Postgres, MongoDB, Redis |
| 📱 **App** | JDK, Kotlin, Gradle, Flutter, Dart, Android Studio, Android platform tools, Node.js, and on macOS Xcode CLT, Swift and CocoaPods |
| ☢️ **NUKE** | The union of all four, with every shared dependency installed exactly once |

Every profile also inherits a core set: Git, Git LFS, GitHub CLI, curl, wget,
jq, make, CMake, OpenSSH, Vim, VS Code, Postman, Docker.

Frameworks such as React, Next.js, Vite and Prisma are deliberately **not**
installed globally. They belong in a project's `package.json`.

---

## Usage

```bash
bento                            # choose a profile interactively
bento --profile web              # skip the menu
bento --profile nuke --dry-run   # see exactly what would happen, change nothing
bento --profile ai --yes         # unattended
bento --plan                      # show the plan and stop
bento --json                      # machine-readable report on stdout
bento --list                      # list the profiles
```

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

Exit codes: `0` success · `1` a dependency failed · `2` usage error ·
`130` you cancelled.

---

## How Bento behaves

**It shows you the plan first.** Nothing on your machine is modified before you
confirm. `--dry-run` never modifies anything at all.

**It is idempotent.** Run it twice and the second run installs nothing, adds no
duplicate PATH entries and rewrites no configuration. Re-running after a
failure is safe.

**It does not reinstall what you have.** An existing tool is left alone unless
its version is below the minimum the profile needs.

**It verifies rather than assumes.** A package manager reporting success is not
proof. Bento only calls something installed once it has run the tool and read
a version out of it. Versions in the report are always observed, never guessed.

**Installed and running are different things.** Bento installs PostgreSQL; it
does not start PostgreSQL unless a profile genuinely requires it. A database
that is installed but stopped is reported as exactly that — a success with a
stopped service, not a failure.

**It says when something is not possible.** Redis has no official Windows
build, and Bento says so rather than installing a third-party fork. Xcode is
macOS-only. Some tools have no reliable automated install on some platforms;
those are reported as skipped with a reason and an official link.

**One failure does not stop the run.** An unrelated dependency that fails is
recorded and the run continues. Anything that depended on it is skipped with
that reason rather than failing confusingly on its own.

**It tells you the truth about your shell.** Environment changes are not
visible to the terminal Bento is running in, and Bento says so instead of
pretending otherwise.

---

## Where software comes from

In order of preference:

1. The platform's package manager — winget, apt, dnf, pacman, zypper, Homebrew, snap
2. The vendor's own release, downloaded over HTTPS and checksum-verified
3. The language ecosystem's package manager — pip, npm, cargo

Bento never pipes a remote script into a shell. Where a vendor's documented
install method is `curl … | sh` — rustup, Ollama, Foundry — Bento fetches the
signed binary or builds from the official repository instead.

Downloads are checksum-verified wherever the vendor publishes checksums, and
Go, Node.js and Flutter releases are resolved from the vendors' own release
metadata at install time, so there are no hardcoded version numbers to go
stale.

---

## Environment configuration

| Platform | What Bento writes |
| --- | --- |
| Windows | `HKCU\Environment` via the registry API, then broadcasts `WM_SETTINGCHANGE`. Never `HKLM`, never `setx` (which truncates a long PATH). |
| Linux, macOS | One clearly-marked block in your shell file (`.bashrc`, `.zshrc`, `config.fish` or `.profile`), regenerated from Bento's own state file so repeated runs converge instead of appending. |

Everything outside the markers is yours and is never touched:

```bash
# >>> bento box >>>
# Managed by Bento Box. Changes between these markers are regenerated.
export GOPATH="/home/dev/go"
case ":$PATH:" in *:"/home/dev/go/bin":*) ;; *) export PATH="/home/dev/go/bin":"$PATH" ;; esac
# <<< bento box <<<
```

Values come from the tools themselves — `GOROOT` from `go env GOROOT`, never
from a guess about where Go was installed.

---

## Reports

Every run writes `~/.bento/report.json` and a full command log to
`~/.bento/logs/`. The JSON report carries the platform, profile, per-dependency
status and observed version, environment changes, service states, errors,
warnings and whether a restart is needed.

```json
{
  "platform": "linux",
  "architecture": "amd64",
  "profile": "web",
  "environment_ready": true,
  "restart_required": false,
  "dependencies": [
    { "name": "node", "status": "INSTALLED", "version": "22.14.0" }
  ],
  "summary": { "installed": 7, "already_installed": 3, "updated": 1, "failed": 0, "skipped": 0 }
}
```

---

## Architecture

```
                    🍱 Bento Box
                         │
                 Standalone Go binary
                         │
             ┌───────────┴───────────┐
        System detector          CLI / UI
             └───────────┬───────────┘
                    Profile resolver
                         │
                   Dependency graph
                         │
                  Package manager adapter
              ┌──────────┼──────────┐
           Windows      Linux      macOS
            winget   apt · dnf     brew
                     pacman        snap
                     zypper
              └──────────┼──────────┘
                    Installation
                         │
                    Environment
                         │
                     Verification
                         │
                      Reporting
```

| Package | Responsibility |
| --- | --- |
| `internal/command` | The only place that runs external programs; mockable |
| `internal/detector` | OS, architecture, distribution, package managers, shell, privileges, GPU |
| `internal/dependency` | The declarative dependency model and status types |
| `internal/catalog` | Loads and validates the embedded dependency data |
| `internal/profiles` | Profile definitions and expansion |
| `internal/resolver` | Graph closure, deduplication, platform filtering, topological order |
| `internal/pkgmanager` | One interface, seven adapters |
| `internal/installer` | Planning, execution, downloads, vendor version resolvers |
| `internal/environment` | PATH and environment variables, idempotently |
| `internal/verifier` | CLI versions, GUI applications, components |
| `internal/services` | Service state, kept separate from installation |
| `internal/reporter` | Terminal report and `report.json` |
| `internal/cli` | Menu, plan, progress, flags |

Dependencies are **data**, not code: `config/dependencies/*.json`, embedded
into the binary with `go:embed` and validated at load time and by tests. Adding
a tool is a data change.

---

## Development

Contributors need Go 1.25 or newer. Users do not.

```bash
git clone https://github.com/Aryan27-max/bento-box.git
cd bento-box
go build ./...
go test ./...
go run ./cmd/bento --dry-run
```

Full command reference, including release builds for every platform, is in
[startcmd.md](startcmd.md). The engineering log is [works.md](works.md).

Unit tests never touch the real system: package managers, command execution
and environment writes are all behind interfaces with mocks. Tests that hit
the network are behind a build tag:

```bash
go test -tags integration ./internal/installer/...
```

---

## Current limitations

Stated plainly, because the alternative is a README that lies:

- **Real installations have been exercised primarily through the dry-run path
  and mocked unit tests.** The full install path is implemented and tested
  against fakes; it has not yet been run end to end on a clean machine of each
  supported OS. CI covers building and testing, not provisioning.
- Package identifiers for less common distributions (openSUSE, Arch) are the
  most likely thing to be wrong. A wrong identifier surfaces as an honest
  `FAILED` with the package manager's own message, not a silent skip.
- Foundry is built from source with cargo on Windows and Linux, which is slow.
- Kotlin on Windows, MongoDB on Linux, Compass on Linux and Swift outside
  macOS are reported as manual installs with an official link, because no
  reliable automated path exists that Bento is willing to take.
- Only `amd64` and `arm64` are supported.
- There is no uninstall command. Bento adds; it does not remove.

## Contributing

Adding a dependency usually means editing one JSON file in
`config/dependencies/`. The catalog is validated by tests, so a malformed
entry, a dangling prerequisite, a plain-HTTP download or a dependency with no
verification strategy will fail `go test ./...` before it reaches anyone.

If a tool cannot be installed reliably on a platform, say so in the data with
a reason and a link. Do not fake support.

## License

MIT. See [LICENSE](LICENSE).
