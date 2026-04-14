# CLAUDE.md - oda-lite (Custom Telegraf Distribution)

## Project overview

This repo builds a custom distribution of [Telegraf](https://github.com/influxdata/telegraf) (oda-lite / OpenGate Device Agent Lite) with proprietary plugins and ships it for multiple platforms via GitHub Actions.

- **Base**: Telegraf v1.35.4 (shallow-cloned at build time)
- **Build modes**:
  - `mini` (default): all Telegraf plugins + custom plugins.
  - `nano`: only plugins referenced by `.conf` files in `config/`.
- **Platforms**: Linux (amd64, arm64, i386, armel, armhf), Windows (amd64, arm64, i386), Darwin (amd64, arm64), FreeBSD (amd64, i386, armv7).

## Repository structure

```
.github/workflows/
  build-telegraf.yml    # Manual build (workflow_dispatch) - binary + configs only
  release.yml           # Tag-triggered release - full packaging (.deb/.rpm/.tar.gz/.zip)
plugins/
  common/system/        # Shared utilities (system metrics, unique ID)
  inputs/
    iface_guard/        # Network interface monitoring (all platforms)
    ssh_guard/          # SSH traffic monitoring (Linux only - uses go-pcap)
    usb_guard/          # USB device monitoring (Linux only - uses go-udev)
    all/                # Plugin registration files (import _ "...")
  outputs/
    og_report/          # OpenGate reporting output
    all/                # Plugin registration files
build.sh                # Main build script (clone, inject plugins, compile)
cicd.sh                 # CI/CD wrapper (calls build.sh with --no-interactive)
dependencies.txt        # Pinned Go module versions for custom plugins
```

## Build scripts

### build.sh

Main build script. Key flags:

| Flag | Description | Default |
|---|---|---|
| `--version` | Telegraf version | 1.35.4 |
| `--mode` | `nano` or `mini` | mini |
| `--config-dir` | Directory with `.conf` files | config |
| `--plugins-dir` | Custom plugins directory | plugins |
| `--dist-dir` | Output directory | . |
| `--go-get` | Extra Go modules (space/comma separated) | |
| `--go-get-file` | File with one dependency per line | |
| `--exclude-plugins` | Blacklist of plugins to exclude (e.g. `inputs/ssh_guard`) | |
| `--keep-source` | Preserve `telegraf_src/` after build | false |
| `--no-interactive` | Skip interactive prompts (CI mode) | false |

### cicd.sh

Thin CI wrapper: creates `--dist-dir`, validates tools, calls `build.sh --no-interactive`. Does NOT package or publish.

## Workflows

### build-telegraf.yml (manual)

- Trigger: `workflow_dispatch` from Actions tab.
- Builds binary + configs per platform matrix, uploads as artifacts.
- No packaging (no .deb/.rpm).

### release.yml (tag push)

- Trigger: tags matching `v*` or `custom-telegraf-*`.
- Matrix groups: `linux-core`, `linux-legacy`, `windows`, `darwin`, `freebsd`.
- Steps: `cicd.sh build` -> `make package` (Telegraf Makefile) -> upload artifacts -> create GitHub Release via `softprops/action-gh-release`.
- `fail-fast: false` so all groups build independently.

## Platform-specific plugin exclusions

Some custom plugins use Linux-only syscalls/libraries and must be excluded on non-Linux:

| Plugin | Reason | Excluded on |
|---|---|---|
| `inputs/ssh_guard` | Uses `github.com/packetcap/go-pcap` (raw packet capture) | windows, darwin, freebsd |
| `inputs/usb_guard` | Uses `github.com/pilebones/go-udev` (netlink/udev) | windows, darwin, freebsd |

The `--exclude-plugins` flag removes both the plugin directory AND the registration file (`<type>/all/<name>.go`) to prevent dangling imports.

## Dependencies (dependencies.txt)

Custom plugin dependencies are pinned in `dependencies.txt`, consumed via `--go-get-file`. Versions must be pinned explicitly to avoid breaking API changes from upstream.

When adding a new dependency:
1. Add it to `dependencies.txt` with `module@version` format.
2. Verify the version is compatible with the plugin code.
3. If the dependency is platform-specific, ensure the plugin is in the exclude list for incompatible platforms.

## Cross-compilation

- All builds use `CGO_ENABLED=0`.
- If a future plugin requires Cgo, adjust the workflow and provide the appropriate toolchain.

## Git conventions

- Tags: `vX.Y.Z` or `custom-telegraf-X.Y.Z` trigger releases.
- Commit messages: short, imperative, English.
