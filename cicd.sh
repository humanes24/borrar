#!/usr/bin/env bash
set -euo pipefail

# Wrapper de CI/CD para preparar el árbol de Telegraf con plugins custom
# usando build.sh. El empaquetado (tar.gz/zip/deb/rpm) lo hace el Makefile
# de Telegraf en los workflows.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

usage() {
  cat <<EOF
Uso: $0 build [opciones]

Prepara el árbol de Telegraf con plugins y dependencias listo para empaquetar.
El empaquetado se realiza con 'make package' en los workflows.

Opciones:
  --version <v>           Versión de Telegraf (ej: 1.35.4)
  --mode <nano|mini>      Modo de compilación (default: mini)
  --config-dir <dir>      Directorio con .conf (default: config)
  --plugins-dir <dir>     Directorio de plugins (default: plugins)
  --dist-dir <dir>        Directorio salida de build.sh (default: dist)
  --go-get <lista>        Dependencias extra para go get (espacio/coma)
  --go-get-file <file>    Fichero con dependencias (una por línea)
  --exclude-plugins <ls>  Lista negra de plugins a excluir del copiado (espacio/coma, p. ej. inputs/ssh_guard)
  --keep-source           No borra 'telegraf_src' al finalizar
EOF
}

cmd="build"
if [[ ${1:-} == "build" ]]; then
  cmd="$1"; shift
fi

TELEGRAF_VERSION="1.35.4"
MODE="mini"
CONFIG_DIR="config"
PLUGINS_DIR="plugins"
DIST_DIR="dist"
GO_GET_LIST=""
GO_GET_FILE=""
KEEP_SOURCE=false
EXCLUDE_PLUGINS=""

PUBLISH=false
RELEASE_TAG=""
RELEASE_NAME=""
RELEASE_NOTES=""
REPO_SLUG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) TELEGRAF_VERSION="$2"; shift 2;;
    --mode) MODE="$2"; shift 2;;
    --config-dir) CONFIG_DIR="$2"; shift 2;;
    --plugins-dir) PLUGINS_DIR="$2"; shift 2;;
    --dist-dir) DIST_DIR="$2"; shift 2;;
    --go-get) GO_GET_LIST+=" $2"; shift 2;;
    --go-get-file) GO_GET_FILE="$2"; shift 2;;
    --keep-source) KEEP_SOURCE=true; shift 1;;
    --exclude-plugins) EXCLUDE_PLUGINS+=" $2"; shift 2;;
    -h|--help) usage; exit 0;;
    *) echo "Opción desconocida: $1"; usage; exit 1;;
  esac
done

# Asegurar que el directorio de distribución exista para que build.sh no falle
mkdir -p "$DIST_DIR"

for cmd_required in go git tar; do
  if ! command -v "$cmd_required" >/dev/null 2>&1; then
    echo "❌ Falta dependencia: $cmd_required"; exit 1
  fi
done

# Build
chmod +x build.sh
BUILD_ARGS=(
  --version "$TELEGRAF_VERSION"
  --mode "$MODE"
  --config-dir "$CONFIG_DIR"
  --plugins-dir "$PLUGINS_DIR"
  --dist-dir "$DIST_DIR"
  --no-interactive
)
if [ -n "$GO_GET_LIST" ]; then BUILD_ARGS+=(--go-get "$GO_GET_LIST"); fi
if [ -n "$GO_GET_FILE" ]; then BUILD_ARGS+=(--go-get-file "$GO_GET_FILE"); fi
if [ -n "$EXCLUDE_PLUGINS" ]; then BUILD_ARGS+=(--exclude-plugins "$EXCLUDE_PLUGINS"); fi
if [ "$KEEP_SOURCE" = true ]; then BUILD_ARGS+=(--keep-source); fi

echo "🧱 Ejecutando build.sh ${BUILD_ARGS[*]}"
./build.sh "${BUILD_ARGS[@]}"

echo "✅ Preparación completada. Árbol listo en 'telegraf_src' y binario/configs en '${DIST_DIR}'."
