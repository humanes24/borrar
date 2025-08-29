#!/usr/bin/env bash
set -euo pipefail

# Wrapper de CI/CD para construir el Telegraf custom usando build.sh,
# empaquetar artefactos y (opcionalmente) publicar una Release en GitHub.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

usage() {
  cat <<EOF
Uso: $0 build [opciones]
     $0 build-and-release [opciones]

Opciones comunes:
  --version <v>           Versión de Telegraf (ej: 1.31.1)
  --mode <nano|mini>      Modo de compilación (default: mini)
  --config-dir <dir>      Directorio con .conf (default: config)
  --plugins-dir <dir>     Directorio de plugins (default: plugins)
  --dist-dir <dir>        Directorio salida de build.sh (default: dist)
  --go-get <lista>        Dependencias extra para go get (espacio/coma)
  --go-get-file <file>    Fichero con dependencias (una por línea)
  --artifact-dir <dir>    Directorio donde dejar los .tar.gz (default: out)

Opciones de release:
  --publish               Publica Release en GitHub (requiere GITHUB_TOKEN)
  --release-tag <tag>     Tag de la release (si no se pasa y se publica, usa versión)
  --release-name <name>   Nombre para la release
  --release-notes <text>  Notas para la release
  --repo <owner/repo>     Repositorio GitHub (auto-detect si no se pasa)
EOF
}

cmd="build"
if [[ ${1:-} == "build" || ${1:-} == "build-and-release" ]]; then
  cmd="$1"; shift
fi

TELEGRAF_VERSION="1.31.1"
MODE="mini"
CONFIG_DIR="config"
PLUGINS_DIR="plugins"
DIST_DIR="dist"
GO_GET_LIST=""
GO_GET_FILE=""
ARTIFACT_DIR="out"

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
    --artifact-dir) ARTIFACT_DIR="$2"; shift 2;;
    --publish) PUBLISH=true; shift 1;;
    --release-tag) RELEASE_TAG="$2"; shift 2;;
    --release-name) RELEASE_NAME="$2"; shift 2;;
    --release-notes) RELEASE_NOTES="$2"; shift 2;;
    --repo) REPO_SLUG="$2"; shift 2;;
    -h|--help) usage; exit 0;;
    *) echo "Opción desconocida: $1"; usage; exit 1;;
  esac
done

mkdir -p "$ARTIFACT_DIR"

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

echo "🧱 Ejecutando build.sh ${BUILD_ARGS[*]}"
./build.sh "${BUILD_ARGS[@]}"

# Datos para el nombre del artefacto
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
ARTIFACT_NAME="telegraf-custom-${TELEGRAF_VERSION}-${MODE}-${GOOS}-${GOARCH}.tar.gz"

# Empaquetado
echo "📦 Empaquetando ${ARTIFACT_NAME} desde '${DIST_DIR}'"
tar -C "$DIST_DIR" -czf "$ARTIFACT_DIR/$ARTIFACT_NAME" .

# Checksum
SHA_FILE="$ARTIFACT_DIR/${ARTIFACT_NAME}.sha256"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$ARTIFACT_DIR" && sha256sum "$ARTIFACT_NAME" > "${SHA_FILE}")
else
  (cd "$ARTIFACT_DIR" && shasum -a 256 "$ARTIFACT_NAME" > "${SHA_FILE}")
fi

echo "ARTIFACT=$ARTIFACT_DIR/$ARTIFACT_NAME"
echo "SHA256_FILE=$SHA_FILE"

if [[ "$cmd" == "build" && "$PUBLISH" != true ]]; then
  exit 0
fi

# Publicación (Release GitHub vía API)
if [ "$PUBLISH" = true ] || [ "$cmd" = "build-and-release" ]; then
  : "${GITHUB_TOKEN:?GITHUB_TOKEN requerido para publicar}"
  if [ -z "$REPO_SLUG" ]; then
    origin_url="$(git config --get remote.origin.url || true)"
    if [[ "$origin_url" =~ github.com[:/](.+/.+)(\.git)?$ ]]; then
      REPO_SLUG="${BASH_REMATCH[1]}"
    else
      echo "❌ No se pudo autodetectar el repo. Usa --repo owner/repo"; exit 1
    fi
  fi
  if [ -z "$RELEASE_TAG" ]; then
    RELEASE_TAG="custom-telegraf-${TELEGRAF_VERSION}-${MODE}"
  fi
  if [ -z "$RELEASE_NAME" ]; then
    RELEASE_NAME="Custom Telegraf ${TELEGRAF_VERSION} (${MODE})"
  fi

  api="https://api.github.com/repos/${REPO_SLUG}"
  auth=( -H "Authorization: Bearer ${GITHUB_TOKEN}" -H "Accept: application/vnd.github+json" )

  echo "📤 Creando/obteniendo release '${RELEASE_TAG}' en ${REPO_SLUG}"
  # Intentar crear release
  create_resp=$(curl -sS -X POST "${api}/releases" "${auth[@]}" \
    -d "$(jq -n --arg tag "$RELEASE_TAG" --arg name "$RELEASE_NAME" --arg body "$RELEASE_NOTES" '{tag_name:$tag,name:$name,body:$body,draft:false,prerelease:false}')" || true)
  # Si ya existe, obtenerla
  upload_url=$(echo "$create_resp" | jq -r '.upload_url? // empty' | sed 's/{?name,label}//')
  release_id=$(echo "$create_resp" | jq -r '.id? // empty')
  if [ -z "$upload_url" ] || [ -z "$release_id" ] || [[ "$create_resp" == *"already_exists"* ]]; then
    get_resp=$(curl -sS "${api}/releases/tags/${RELEASE_TAG}" "${auth[@]}")
    upload_url=$(echo "$get_resp" | jq -r '.upload_url' | sed 's/{?name,label}//')
    release_id=$(echo "$get_resp" | jq -r '.id')
  fi
  if [ -z "$upload_url" ] || [ -z "$release_id" ]; then
    echo "❌ No se pudo obtener la release para subir assets"; exit 1
  fi

  # Subir artefactos
  for file in "$ARTIFACT_DIR/$ARTIFACT_NAME" "$SHA_FILE"; do
    name="$(basename "$file")"
    echo "⬆️  Subiendo asset $name"
    curl -sS -X POST "${upload_url}?name=${name}" \
      -H "Content-Type: application/octet-stream" \
      "${auth[@]}" \
      --data-binary @"$file" >/dev/null
  done
  echo "✅ Release publicada: ${REPO_SLUG} tag ${RELEASE_TAG}"
fi
