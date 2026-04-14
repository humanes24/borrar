# oda-lite

Custom distribution of [Telegraf](https://github.com/influxdata/telegraf) (OpenGate Device Agent Lite) with proprietary plugins for device monitoring and reporting.

## Features

- **Custom plugins**: SSH traffic monitoring, USB device monitoring, network interface monitoring, OpenGate reporting.
- **Multi-platform**: Linux (amd64, arm64, i386, armel, armhf), Windows (amd64, arm64, i386), Darwin (amd64, arm64), FreeBSD (amd64, i386, armv7).
- **Two build modes**: `mini` (all plugins) and `nano` (only plugins referenced by config files).
- **Automated releases**: tag push triggers packaging for all platforms (.deb, .rpm, .tar.gz, .zip).

## Quick start

### Manual build (local)

```bash
# Basic build (mini mode, current platform)
./build.sh --version 1.35.4 --mode mini --plugins-dir plugins --dist-dir dist

# With extra dependencies
./build.sh --version 1.35.4 --mode mini --plugins-dir plugins --dist-dir dist \
  --go-get-file dependencies.txt

# Exclude Linux-only plugins (e.g. building for macOS)
./build.sh --version 1.35.4 --mode mini --plugins-dir plugins --dist-dir dist \
  --go-get-file dependencies.txt --exclude-plugins "inputs/ssh_guard,inputs/usb_guard"
```

### CI/CD build

```bash
./cicd.sh build --version 1.35.4 --mode mini --dist-dir dist --go-get-file dependencies.txt
```

### Run the built binary

```bash
./run_oda_lite.sh                                    # default config
./run_oda_lite.sh --config-dir /path/to/configs      # custom config dir
./run_oda_lite.sh --test --once                      # pass flags to Telegraf
```

## Releasing

Push a tag to trigger automatic packaging and GitHub Release creation:

```bash
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

Supported tag patterns: `v*`, `custom-telegraf-*`.

## Custom plugins

| Plugin | Type | Platform | Description |
|---|---|---|---|
| `ssh_guard` | input | Linux only | SSH traffic monitoring via packet capture |
| `usb_guard` | input | Linux only | USB device connect/disconnect monitoring |
| `iface_guard` | input | All | Network interface status monitoring |
| `og_report` | output | All | OpenGate platform reporting |

## Dependencies

External Go modules required by custom plugins are pinned in `dependencies.txt`. Add new dependencies there with explicit versions (`module@version`) to ensure reproducible builds.

## Project structure

```
.github/workflows/       GitHub Actions (manual build + tag release)
plugins/                 Custom Telegraf plugins
  inputs/                Input plugins + registration files
  outputs/               Output plugins + registration files
  common/                Shared utilities
build.sh                 Main build script
cicd.sh                  CI/CD wrapper
dependencies.txt         Pinned Go module dependencies
```
