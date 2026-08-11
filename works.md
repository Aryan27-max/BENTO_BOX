# Bento Box — Work Log

A running record of real work. Nothing is marked complete here unless it has
been run and observed to work, and anything that has *not* been exercised is
called out explicitly.

---

## 2026-08-11

### Starting state

The repository was empty — no files, no git history, no Go module.

One thing worth recording, because it is a trap: `git status` in this directory
initially reported a clean tree on `main`. That was not this project. There is
a `.git` directory at `C:\`, so git was walking up and operating on the whole
drive. `git init -b main` was run here first so that no git command in this
project can touch the drive root.

Toolchain on the development machine: Go 1.26.4, Windows 11 (build 26200),
amd64.

### What was built

The whole pipeline, from an empty directory to six cross-compiled release
binaries. Phases 1 through 10 of the plan are implemented; the honest caveats
are in **Not yet proven** below.

| Package | Purpose |
| --- | --- |
| `internal/command` | The only code that runs external programs. Everything else goes through the `Runner` interface, which is what makes the whole suite testable without touching the machine. |
| `internal/version` | Parsing and comparing the version strings real tools print. |
| `internal/dependency` | The declarative model: categories, methods, statuses, actions, outcomes. |
| `internal/catalog` | Loads and validates the embedded JSON catalog. |
| `internal/profiles` | Profile definitions, aliases, and expansion (this is where NUKE becomes a union). |
| `internal/resolver` | Graph closure, deduplication, platform and hardware filtering, topological ordering. |
| `internal/detector` | OS, architecture, distribution, package managers, shell, privileges, GPU. |
| `internal/pkgmanager` | One `Manager` interface; adapters for winget, apt, dnf, pacman, zypper, brew, snap. |
| `internal/verifier` | CLI version checks, GUI application checks by path, sub-component checks. |
| `internal/environment` | PATH and environment variables, idempotently, per platform. |
| `internal/services` | Service state observation, kept separate from installation. |
| `internal/installer` | Planning, execution, downloads, archive extraction, vendor version resolvers. |
| `internal/reporter` | The terminal box report and `report.json`. |
| `internal/logging` | The command audit trail, with secret redaction. |
| `internal/cli` | Menu, plan, confirmation, progress, flags. |
| `internal/paths`, `internal/textwidth` | Small shared helpers that turned out to be load-bearing. |

Dependency definitions are data in `config/dependencies/*.json`, embedded with
`go:embed`. **69 dependencies** across five profiles.

### Testing

```
go build ./...        clean
go vet ./...          clean
gofmt -l .            clean
go test ./...         191 tests, all passing
```

Per-package coverage of statements:

| Package | Coverage |
| --- | --- |
| version | 91.8% |
| catalog | 91.1% |
| resolver | 88.7% |
| detector | 85.7% |
| verifier | 83.7% |
| reporter | 82.3% |
| services | 77.6% |
| pkgmanager | 75.5% |
| environment | 62.7% |
| installer | 49.5% |
| cli | 38.1% |

The installer and CLI numbers are lower because the parts that genuinely touch
the OS and the terminal are exercised by hand rather than by unit tests. The
decision logic in both — planning, fallback, ordering, flag parsing, key
decoding — is covered.

No unit test installs software, writes to the registry, or calls the network.

### Integration tests (network, read-only)

`go test -tags integration -run TestLive ./internal/installer/...` — passing.

These call the real vendor endpoints to confirm Bento's parsing still matches
what the vendors publish. Observed today:

```
go       1.26.5   for all six OS/arch combinations, with SHA-256 checksums
node     24.19.0  (LTS) for linux/amd64, darwin/arm64, windows/amd64
flutter  3.44.9   for linux/amd64, darwin/arm64, windows/amd64
```

This matters because it is the check that no version number is hardcoded
anywhere: those figures came from go.dev, nodejs.org and Google's release
manifest at test time.

### Real run on this machine

`bento --profile web --dry-run` and `--profile nuke --dry-run` were run against
the actual Windows 11 development machine. Detection was correct across the
board — it found Git 2.51.0, Go 1.26.4, Node 22.23.1, npm 10.9.8, pnpm 10.16.0,
Bun 1.3.4, Python 3.14.6, Docker 29.4.0, PostgreSQL 18.1.0, MongoDB 8.3.4,
Rust 1.93.1, VS Code 1.132.0 and 18 others, with the versions those tools
actually report.

It also correctly reported Redis as `UNSUPPORTED` on Windows with the real
reason, skipped CUDA because this machine has no NVIDIA GPU, and identified
Kotlin and Swift as manual installs on Windows with official links.

### Bugs found and fixed

**1. A test wrote to the real Windows registry.**
The most serious problem of the day, and exactly the failure the "tests must
not touch the real system" rule exists to prevent. The environment package
chose its platform behaviour by Go build tag rather than by the target OS, so
a `Manager` configured for Linux still wrote to `HKCU\Environment` when the
test ran on a Windows host.

The damage was real and worse than it first looked. Three variables were
written into the development machine's user environment:

```
GOROOT       = /usr/local/go                       (a mock value from a test)
GOPATH       = …\Temp\TestApplierResolvesValues…\001\go   (a deleted temp dir)
ANDROID_HOME = …\Temp\TestApplierSetsVariable…\001\Android\Sdk
```

The `GOPATH` one was actively harmful: it pointed at a temporary directory
that no longer existed, which would have sent every subsequent `go` command's
module cache somewhere nonsensical.

Fixed by introducing an explicit `backend` interface selected by the *target*
OS rather than the compilation target: `platformBackend("linux")` returns the
state-file backend even on a Windows build, so a cross-platform test can never
reach the registry. All three stray values were deleted, and `go env` confirms
the machine is back to its real `C:\Program Files\Go` and `C:\Users\gupta\go`.

Verified afterwards with `go clean -testcache && go test ./...` followed by a
registry check: the full suite now leaves `HKCU\Environment` completely
untouched.

The lesson worth keeping: build tags describe where code *compiles*, not what
it should *act on*. Anything that mutates a machine needs the target chosen by
data, not by `//go:build`.

**2. Verification reported missing Python libraries as installed.**
Libraries are verified with `python -c "import torch"`. Presence was being
decided by whether the *executable* resolved, and `python` always resolves — so
PyTorch and TensorFlow were reported `ALREADY_INSTALLED` on a machine that has
neither. Fixed: a check that exits non-zero and yields no version now means
absent. Confirmed against the real machine, where both now correctly appear in
the install plan while NumPy, pandas and scikit-learn (genuinely installed)
still verify with real versions.

**3. Emoji broke the report box.**
Widths were measured with `utf8.RuneCountInString`, so 🍱 and 🌐 counted as one
column when a terminal gives them two, and every line containing one came out a
character narrow. Fixed with `internal/textwidth`, which handles emoji blocks,
East Asian wide ranges, zero-width joiners, and the variation-selector case
where ⛓ (one column) becomes ⛓️ (two). The report is now rectangular; there is a
test asserting that for both plain and coloured output.

**4. ANSI escapes leaked as literal text.**
`\r\x1b[K` was written unconditionally, so `--no-color` output contained a
visible `[K`. Fixed with `UI.ClearLine`, which only emits escapes when colour
is actually enabled.

**5. Dry runs emitted nonsense warnings.**
Environment changes not written during a dry run were reported as warnings, and
the message concatenated name and value without a separator
(`GOROOTC:\Program Files\Go`). Both fixed.

### Decisions

**Two dependencies, both `golang.org/x`.** `x/sys` for the Windows registry and
console APIs, `x/term` for Unix raw mode. Everything else is standard library,
including the catalog format (JSON, not YAML, to avoid a third-party parser)
and the entire TUI.

**No `curl | sh`, ever.** Several vendors document that as their install
method. Rustup is installed by downloading the signed `rustup-init` binary and
running it with `--no-modify-path`; Ollama on Linux comes from the official
release tarball; Foundry is built from its official repository with cargo. This
costs some speed and is worth it.

**Vendor metadata instead of hardcoded versions.** Go, Node and Flutter are
resolved from go.dev's release index, nodejs.org's `index.json` and Google's
release manifest at install time, with the checksums those endpoints publish.
No version number in Go source, none in the catalog.

**Installed and running are separate states.** Bento installs PostgreSQL and
reports the service as stopped without calling that a failure. It starts a
service only when the catalog marks it required.

**A package manager reporting success is not proof.** Nothing is marked
installed until the tool has been run and a version read from it. An install
that succeeds but delivers a version below the profile's minimum is treated as
a failed candidate, and Bento moves to the next source — which is what makes
`apt install nodejs` on an older Debian fall through to the official tarball.

**Fallbacks live in data.** Each platform lists installation candidates in
order and Bento tries them in sequence, so "snap, else the vendor package, else
the distro package" is a JSON array, not a branch in Go.

**Node globals on Linux use a Bento-owned prefix.** The system prefix under
`/usr` is not writable without root, and running `npm -g` under sudo leaves
root-owned files in the user's cache. Bento sets `npm_config_prefix` to
`~/.bento/npm` and puts that on PATH instead.

**pip installs to the user site, with one documented retry.** Modern Debian,
Ubuntu and Fedora mark their system Python as externally managed (PEP 668) and
refuse even `--user`. Bento retries once with `--break-system-packages`, which
stays within the user's own site-packages, and records a warning saying so.

### Platform-specific findings

- Windows 11 identifies itself as version 10.0; only a build number of 22000 or
  higher distinguishes it from Windows 10. Handled and tested.
- `whoami /groups` containing SID `S-1-16-12288` is a reliable elevation check.
- `setx` truncates PATH at 1024 characters, so Bento uses the registry API
  directly and preserves `REG_EXPAND_SZ` when that is the existing value type.
- On Windows, arrow keys are not delivered to a raw read unless
  `ENABLE_VIRTUAL_TERMINAL_INPUT` is set; `x/term.MakeRaw` does not set it, so
  the console mode is configured explicitly.
- `npm` is `npm.cmd` on Windows, so executable resolution tries `.exe`, `.cmd`
  and `.bat`.
- A removed-but-not-purged Debian package still has a dpkg entry; only the full
  `install ok installed` status means it is really there.
- `winget list` can exit zero while reporting that it found nothing.
- Homebrew refuses to run under sudo, so the brew adapter never elevates
  regardless of what a dependency asks for.
- Go has no xz decompressor in its standard library. Flutter's Linux archive is
  `.tar.xz`, so that one case shells out to `tar`, which every system shipping
  an xz archive has.

### Release verification

All six targets cross-compiled from this machine with `CGO_ENABLED=0`:

```
bento-windows-amd64.exe   7.4M
bento-windows-arm64.exe   6.7M
bento-linux-amd64         7.1M
bento-linux-arm64         6.6M
bento-darwin-amd64        7.2M
bento-darwin-arm64        6.7M
```

`file dist/bento-linux-amd64` reports **statically linked**, confirming the
core promise: a user runs one file and needs no runtime of any kind.
`./dist/bento-windows-amd64.exe --version` printed `bento 0.1.0`, confirming
the ldflags version injection works.

`scripts/build-release.sh 0.1.0` ran end to end and produced `checksums.txt`.

### Not yet proven

Stated plainly so nobody reads more into this log than it says.

- **No end-to-end installation has been performed on a clean machine.** The
  install path is implemented and tested against fakes, and the planning and
  verification halves have been exercised against a real Windows machine via
  `--dry-run`. Actually installing software onto this machine was out of scope
  for today. Until someone runs Bento on a fresh VM per OS, treat the install
  path as implemented-but-unproven.
- Package identifiers for openSUSE and Arch are the least certain part of the
  catalog. A wrong identifier fails honestly with the package manager's own
  message rather than silently skipping, but it will fail.
- The GitHub Actions workflows have not run, because nothing has been pushed to
  GitHub yet. They are written against the same commands that were run locally.
- `internal/logging` and `internal/command` have no direct unit tests; both are
  exercised throughout the other suites.

### Next

1. Run Bento end to end on fresh VMs: Ubuntu 24.04, Fedora 41, Arch, macOS,
   Windows 11. Record real results here.
2. Push to GitHub and confirm CI and the release workflow actually run.
3. Verify the openSUSE and Arch package identifiers against real systems.
4. Consider a `--only <dependency>` flag; it would make end-to-end testing of a
   single install path much cheaper than running a whole profile.
