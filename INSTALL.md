# Installing `ga4`

Pick the path that fits your platform. All install methods deliver the same statically-linked `ga4` binary.

## macOS / Linux — one-liner

```sh
curl -fsSL https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.sh | sh
```

The installer detects your OS (`linux` / `darwin`) and architecture (`amd64` / `arm64`), downloads the latest release archive plus `checksums.txt`, verifies the SHA-256, and installs `ga4` to `/usr/local/bin` (if writable) or `~/.local/bin`.

Pin a version or override the install location:

```sh
GA4_VERSION=v1.2.3 INSTALL_DIR="$HOME/bin" \
  curl -fsSL https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.sh | sh
```

Run `sh install.sh --help` for the full list of options.

## Windows — one-liner (PowerShell 5.1+)

```powershell
irm https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.ps1 | iex
```

Installs `ga4.exe` to `%LOCALAPPDATA%\Programs\ga4\` (no admin required). Override with environment variables:

```powershell
$env:GA4_VERSION = 'v1.2.3'
$env:INSTALL_DIR = "$env:USERPROFILE\bin"
irm https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.ps1 | iex
```

> The first run may show a SmartScreen warning because the binary is not (yet) Authenticode-signed. Choose "Run anyway" — or verify the SHA-256 manually (see below).

## Manual download

Grab the archive for your platform from the [Releases page](https://github.com/KLIXPERT-io/ga4-cli/releases/latest):

| Platform        | Archive                                |
| --------------- | -------------------------------------- |
| Linux amd64     | `ga4_<version>_linux_amd64.tar.gz`     |
| Linux arm64     | `ga4_<version>_linux_arm64.tar.gz`     |
| macOS amd64     | `ga4_<version>_darwin_amd64.tar.gz`    |
| macOS arm64     | `ga4_<version>_darwin_arm64.tar.gz`    |
| Windows amd64   | `ga4_<version>_windows_amd64.zip`      |

Extract, then move `ga4` (or `ga4.exe`) into a directory on your `$PATH`.

## With a Go toolchain

```sh
go install github.com/KLIXPERT-io/ga4-cli/cmd/ga4@latest
```

The binary lands in `$(go env GOBIN)` (or `$(go env GOPATH)/bin`). Note: this build does not embed the release tag in `ga4 --version`.

## From source

```sh
git clone https://github.com/KLIXPERT-io/ga4-cli.git
cd ga4-cli
make build      # produces ./ga4
make install    # installs via `go install`
```

## Verifying checksums manually

Every release ships a `checksums.txt` file alongside the archives. To verify before installing:

```sh
curl -fsSLO https://github.com/KLIXPERT-io/ga4-cli/releases/download/v1.2.3/ga4_1.2.3_linux_amd64.tar.gz
curl -fsSLO https://github.com/KLIXPERT-io/ga4-cli/releases/download/v1.2.3/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
```

## Pinning a version

```sh
GA4_VERSION=v1.2.3 curl -fsSL https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.sh | sh
```

```powershell
$env:GA4_VERSION = 'v1.2.3'
irm https://raw.githubusercontent.com/KLIXPERT-io/ga4-cli/refs/heads/main/install.ps1 | iex
```

## Auto-Update

Once installed, `ga4` keeps itself current. On every invocation a background goroutine checks the GitHub Releases API at most once per 24 hours; if a newer stable tag is published and the running binary is writable, it downloads the matching archive, verifies the SHA-256 against `checksums.txt`, and atomically swaps the binary in place. The current command is unaffected — the next `ga4` invocation runs the new version.

### Disabling auto-update

Two equivalent opt-outs:

```bash
export GA4_NO_UPDATE=1
```

Or in `~/.config/ga4/config.toml`:

```toml
auto_update = false
```

When either is set, no network requests are made and `update-state.json` is not touched.

### Managed installs are skipped automatically

`ga4` detects package-managed binaries by install-path prefix and never auto-updates them — updates come through the package manager instead. The detected prefixes are:

- `/opt/homebrew`, `/usr/local/Cellar` (Homebrew)
- `/home/linuxbrew` (Linuxbrew)
- `/snap` (Snap)
- `/var/lib/flatpak` (Flatpak)
- `C:\ProgramData\chocolatey` (Chocolatey)
- `C:\Users\*\scoop` (Scoop)
- `C:\Program Files`

A binary that is not writable by the current user (or owned by a different uid on unix) is also skipped.

### Inspecting / forcing updates

```bash
ga4 update status   # current + latest version, last check time, enabled state (with reason if disabled)
ga4 update check    # force a check now, bypassing the 24h throttle
ga4 update apply    # force download + atomic swap to the latest version
```

`update status` also prints the resolved install path and the last-installed version recorded in state. `update check` and `update apply` still respect the opt-out and managed-install guards.

### Post-update notice

After a successful background update, the next `ga4` command prints a one-line notice to stderr before its normal output:

```
ga4: updated to vX.Y.Z (was vA.B.C)
```

Suppress it with `GA4_NO_UPDATE_NOTICE=1`.

## Cutting a release (maintainers)

The release version lives in the [`VERSION`](./VERSION) file at the repo root. To ship a new release:

1. Bump `VERSION` (e.g. `0.1.0` → `0.2.0`) and merge to `main`.
2. The `Auto Tag & Release` workflow reads the file, creates a matching `vX.Y.Z` git tag, and triggers the release pipeline (`release.yml`).
3. GoReleaser builds the five archives and publishes a GitHub Release with `checksums.txt`.

Manual fallback: `git tag v0.2.0 && git push --tags` runs the same release pipeline directly.

## Uninstalling

Delete the binary:

```sh
rm "$(command -v ga4)"
```

```powershell
Remove-Item "$env:LOCALAPPDATA\Programs\ga4\ga4.exe"
```
