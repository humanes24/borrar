#!/usr/bin/env bash
set -euo pipefail

# Valores por defecto
TELEGRAF_VERSION="1.35.4"
CONFIG_DIR="config"
CUSTOM_PLUGINS_DIR="plugins"
MODE="mini"  # mini por defecto
DIST_DIR="." # directorio destino de distribución (por defecto, raíz)
# Flags CI/no interactivo y dependencias extra (go get)
NO_INTERACTIVE=false
GO_GET_LIST=""
GO_GET_FILE=""
KEEP_SOURCE=false
EXCLUDE_PLUGINS=""

usage() {
    echo "Uso: $0 [--version <telegraf_version>] [--config-dir <dir_config>] [--plugins-dir <dir_plugins>] [--mode <nano|mini>] [--dist-dir <dir_destino>] [--no-interactive] [--go-get <mod@ver ...>] [--go-get-file <ruta>]"
    echo
    echo "Argumentos (opcional, se usan valores por defecto si no se especifican):"
    echo "  --version       Versión de Telegraf a compilar (default: $TELEGRAF_VERSION)"
    echo "  --config-dir    Directorio con ficheros de configuración de Telegraf (.conf) (default: $CONFIG_DIR)"
    echo "  --plugins-dir   Directorio con los plugins custom (default: $CUSTOM_PLUGINS_DIR)"
    echo "  --mode          Tipo de compilación: nano (solo plugins de configs) o mini (todos los plugins + custom) (default: $MODE)"
    echo "  --dist-dir      Directorio de salida para binario y configs (default: $DIST_DIR)"
    echo "  --no-interactive  Desactiva prompts interactivos (pensado para CI)"
    echo "  --go-get        Lista de módulos a añadir (separados por espacio o coma). Repetible"
    echo "  --go-get-file   Fichero con una dependencia por línea para 'go get'"
    echo "  --exclude-plugins Lista negra de plugins custom a no copiar (separados por espacio o coma, e.g. inputs/ssh_guard)"
    echo "  --help          Muestra esta ayuda"
    echo "  --keep-source   No elimina el árbol clonado (telegraf_src) al finalizar"
    echo
    echo "Ejemplo de uso:"
    echo "  $0 --version 1.31.0 --config-dir /ruta/configs --plugins-dir /ruta/mis_plugins --mode mini --dist-dir dist"
    exit 0
}

# Comprobar dependencias esenciales al inicio
for cmd in make go git; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "❌ Error: '$cmd' no está instalado. Instálalo antes de ejecutar este script."
        exit 1
    fi
done

# Parse CLI y sobrescribir valores por defecto si se pasan argumentos
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version|-v)
      TELEGRAF_VERSION="$2"
      shift 2
      ;;
    --config-dir|-c)
      CONFIG_DIR="$2"
      shift 2
      ;;
    --plugins-dir|-p)
      CUSTOM_PLUGINS_DIR="$2"
      shift 2
      ;;
    --dist-dir|-d)
      DIST_DIR="$2"
      shift 2
      ;;
    --mode|-m)
      MODE="$2"
      shift 2
      ;;
    --no-interactive)
      NO_INTERACTIVE=true
      shift 1
      ;;
    --go-get)
      # Acepta valores separados por espacios o comas
      if [ -n "${2:-}" ]; then
        GO_GET_LIST+=" ${2}"
        shift 2
      else
        echo "❌ '--go-get' requiere un argumento"
        exit 1
      fi
      ;;
    --go-get-file)
      if [ -n "${2:-}" ]; then
        GO_GET_FILE="$2"
        shift 2
      else
        echo "❌ '--go-get-file' requiere una ruta de fichero"
        exit 1
      fi
      ;;
    --help|-h)
      usage
      ;;
    --keep-source)
      KEEP_SOURCE=true
      shift 1
      ;;
    --exclude-plugins)
      if [ -n "${2:-}" ]; then
        EXCLUDE_PLUGINS+=" ${2}"
        shift 2
      else
        echo "❌ '--exclude-plugins' requiere al menos una ruta de plugin (p. ej. inputs/ssh_guard)"
        exit 1
      fi
      ;;
    *)
      echo "❌ Opción desconocida: $1"
      usage
      ;;
  esac
done

# Comprobación temprana: el directorio destino debe existir
if [ ! -d "$DIST_DIR" ]; then
    echo "ℹ️ El directorio de destino '$DIST_DIR' no existe. Créalo y vuelve a ejecutar el script."
    exit 1
fi

# Ajuste de modo según existencia de configs
ORIGINAL_MODE="$MODE"
if [ ! -d "$CONFIG_DIR" ]; then
    if [ "$ORIGINAL_MODE" = "nano" ]; then
        echo "⚠️  Advertencia: el directorio de configuración '$CONFIG_DIR' no existe; forzando modo 'mini' (ignorando 'nano')."
    else
        echo "ℹ️ El directorio de configuración '$CONFIG_DIR' no existe; usando modo 'mini'."
    fi
    MODE="mini"
fi

if [ ! -d "$CUSTOM_PLUGINS_DIR" ]; then
    echo "❌ Error: el directorio de plugins '$CUSTOM_PLUGINS_DIR' no existe."
    exit 1
fi

# Clonar Telegraf
REPO_URL="https://github.com/influxdata/telegraf.git"
CLONE_DIR="telegraf_src"
REPO_ROOT="$(pwd)"

# Normalizar ruta de fichero de dependencias (si es relativa, hacerla absoluta respecto al repo root)
if [ -n "$GO_GET_FILE" ] && [[ ! "$GO_GET_FILE" = /* ]]; then
  GO_GET_FILE="${REPO_ROOT%/}/$GO_GET_FILE"
fi

if [ -d "$CLONE_DIR" ]; then
    echo "ℹ️ Eliminando directorio previo $CLONE_DIR"
    rm -rf "$CLONE_DIR"
fi

echo "📥 Clonando Telegraf versión $TELEGRAF_VERSION..."
git clone --branch "v${TELEGRAF_VERSION}" --depth 1 "$REPO_URL" "$CLONE_DIR"

# Copiar configs
PLUGINS_TELEGRAF_DIR="plugins_conf"
PLUGINS_CONF_DIR="$CLONE_DIR/$PLUGINS_TELEGRAF_DIR"
mkdir -p "$PLUGINS_CONF_DIR"

echo "📂 Preparando configuraciones en $PLUGINS_CONF_DIR..."
if [ -d "$CONFIG_DIR" ] && ls "$CONFIG_DIR"/*.conf >/dev/null 2>&1; then
  cp -r "$CONFIG_DIR"/*.conf "$PLUGINS_CONF_DIR"/
else
  echo "ℹ️ No se encontraron configuraciones en '$CONFIG_DIR'. Continuando sin copiar .conf."
fi

# Copiar plugins custom al árbol "plugins/"
echo "📂 Copiando plugins custom al árbol de Telegraf..."
cp -a "$CUSTOM_PLUGINS_DIR"/. "$CLONE_DIR/plugins/"

# Excluir plugins solicitados (lista negra)
if [ -n "$EXCLUDE_PLUGINS" ]; then
    echo "⛔ Excluyendo plugins custom (lista negra): $EXCLUDE_PLUGINS"
    echo "$EXCLUDE_PLUGINS" | tr ',\t ' '\n\n\n' | while IFS= read -r rel; do
        [ -z "$rel" ] && continue
        # Sanitizar rutas peligrosas
        case "$rel" in
          /*|*..*|"") echo "   ⚠️  Ruta insegura u vacía, se omite: '$rel'"; continue ;;
        esac
        target="$CLONE_DIR/plugins/$rel"
        if [ -e "$target" ]; then
            rm -rf "$target"
            echo "   - Excluido $rel"
        else
            echo "   - No encontrado (ya ausente): $rel"
        fi
        # Eliminar también el fichero de registro en <tipo>/all/<nombre>.go
        plugin_type="${rel%%/*}"
        plugin_name="${rel##*/}"
        reg_file="$CLONE_DIR/plugins/${plugin_type}/all/${plugin_name}.go"
        if [ -f "$reg_file" ]; then
            rm -f "$reg_file"
            echo "   - Registro eliminado: ${plugin_type}/all/${plugin_name}.go"
        fi
    done
fi

# Interactivo: añadir librerías al go.mod
echo "📦 Añadir dependencias adicionales al go.mod (opcional)"
cd "$CLONE_DIR"

# Si se pasa --go-get-file, concatenamos su contenido a la lista
if [ -n "$GO_GET_FILE" ]; then
    if [ ! -f "$GO_GET_FILE" ]; then
        echo "❌ Error: fichero no encontrado para --go-get-file: $GO_GET_FILE"
        exit 1
    fi
    echo "📝 Leyendo dependencias desde archivo: $GO_GET_FILE"
    count=0
    while IFS= read -r raw || [ -n "$raw" ]; do
        # Quitar comentarios y espacios en blanco
        line="${raw%%#*}"
        line="$(printf '%s' "$line" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
        [ -z "$line" ] && continue
        GO_GET_LIST+=" $line"
        count=$((count+1))
    done < "$GO_GET_FILE"
    echo "🧾 Dependencias cargadas: $count"
fi

# Normalizamos separadores (comas y espacios) a saltos de línea y ejecutamos go get
if [ -n "$GO_GET_LIST" ]; then
    echo "➕ Añadiendo dependencias vía --go-get/--go-get-file"
    echo "$GO_GET_LIST" | tr ',\t ' '\n\n\n' | while IFS= read -r lib; do
        [ -z "$lib" ] && continue
        echo "   go get $lib"
        go get "$lib"
    done
elif [ "$NO_INTERACTIVE" = true ] || [ -n "${CI:-}" ]; then
    echo "ℹ️ Modo no interactivo: omitiendo prompt de dependencias adicionales"
else
    echo "   Pega las librerías que necesites añadir. Presiona Enter para confirmar cada iteración."
    echo "   Presiona Enter sin escribir nada para finalizar."
    while true; do
        read -r -p "Agregar librerías (una o varias, separadas por Enter, iteración final Enter solo): " input
        if [ -z "$input" ]; then
            break
        fi
        while IFS= read -r lib; do
            [ -z "$lib" ] && continue
            echo "➕ Añadiendo $lib al go.mod..."
            go get "$lib"
        done <<< "$input"
    done
fi

# Limpiar y preparar dependencias
go mod tidy

# Compilar herramientas
echo "🛠 Compilando herramientas build_tools..."
make build_tools

# Compilación según modo
TELEGRAF_CONF=""
if [ "$MODE" = "nano" ]; then
    echo "⚡ Compilación NANO: solo plugins referenciados en los .conf de $CONFIG_DIR"
    ./tools/custom_builder/custom_builder --config-dir "$PLUGINS_TELEGRAF_DIR"

elif [ "$MODE" = "mini" ]; then
    echo "⚡ Compilación MINI: todos los plugins de Telegraf + plugins custom"

    # Crear archivo vacío de manera segura (se limpia su contenido si ya existe)
    TELEGRAF_CONF="$PLUGINS_TELEGRAF_DIR/telegraf_all.conf"
    > "$TELEGRAF_CONF"

    MINI_SKIP_PLUGINS=("outputs.all" "inputs.all" "inputs.example" "processors.all" "parsers.xpath" "parsers.all" "serializers.all" "secretstores.all" "aggregators.all")

    for type in inputs processors outputs aggregators parsers secretstores serializers; do
        for plugin_dir in "plugins/$type/"*; do
            if [ -d "$plugin_dir" ]; then
                plugin_name=$(basename "$plugin_dir")
                full_name="$type.$plugin_name"

                # Saltar meta-paquetes (all/) y plugins no válidos para custom_builder
                skip=false
                for ex in "${MINI_SKIP_PLUGINS[@]}"; do
                    if [ "$full_name" = "$ex" ]; then
                        skip=true
                        break
                    fi
                done
                $skip && continue

                echo "[[$full_name]]" >> "$TELEGRAF_CONF"
            fi
        done
    done


    echo "📄 Archivo de configuración generado para MINI: $TELEGRAF_CONF"

    ./tools/custom_builder/custom_builder --config-dir "$PLUGINS_TELEGRAF_DIR" --config "$TELEGRAF_CONF"

else
    echo "❌ Modo desconocido: $MODE"
    exit 1
fi

# Preparar distribución en el directorio elegido
cd "$REPO_ROOT"

# Copiar binario compilado
TARGET_GOOS="${GOOS:-$(go env GOOS)}"
BIN_NAME="telegraf"
if [ "$TARGET_GOOS" = "windows" ]; then BIN_NAME="telegraf.exe"; fi
if [ -f "$CLONE_DIR/$BIN_NAME" ]; then
  echo "📦 Copiando binario a destino: $DIST_DIR/$BIN_NAME"
  cp -f "$CLONE_DIR/$BIN_NAME" "$DIST_DIR/$BIN_NAME"
else
  echo "❌ No se encontró el binario compilado en $CLONE_DIR/$BIN_NAME"
  exit 1
fi

# Preparar y copiar configuraciones destinadas a runtime
DEST_CONF_DIR="$DIST_DIR/plugins_conf"
echo "📂 Preparando carpeta de configuración en destino: $DEST_CONF_DIR"

# Utilidades de ruta
to_abs() {
  case "$1" in
    /*) printf "%s" "$1" ;;
    *) printf "%s/%s" "$REPO_ROOT" "$1" ;;
  esac
}
normalize_path() {
  # Colapsa '/./' segmentos y elimina barra final
  local p="$1"
  p="${p%/}"
  # reemplaza cualquier '/./' por '/'
  while [[ "$p" == *"/./"* ]]; do p="${p//\/\.\//\/}"; done
  printf "%s" "$p"
}
same_dir() {
  local s="$1" d="$2"
  # Preferir comparación por inode si existen
  if [ -d "$s" ] && [ -d "$d" ] && [ "$s" -ef "$d" ]; then
    return 0
  fi
  [ "$(normalize_path "$s")" = "$(normalize_path "$d")" ]
}

ABS_SRC_CONF="$(to_abs "$CONFIG_DIR")"
ABS_DEST_CONF="$(to_abs "$DEST_CONF_DIR")"

if same_dir "$ABS_SRC_CONF" "$ABS_DEST_CONF"; then
  echo "ℹ️ Origen de configs y destino coinciden ('$DEST_CONF_DIR'); no se elimina ni copia."
  mkdir -p "$DEST_CONF_DIR"
else
  rm -rf "$DEST_CONF_DIR"
  mkdir -p "$DEST_CONF_DIR"
  if [ -d "$CONFIG_DIR" ] && ls "$CONFIG_DIR"/*.conf >/dev/null 2>&1; then
    cp -f "$CONFIG_DIR"/*.conf "$DEST_CONF_DIR/"
  else
    echo "ℹ️ Sin archivos .conf para copiar a destino."
  fi
fi

## Generar script de ejecución run_oda_lite.sh en el directorio raíz del repo
## El script detectará si DIST_DIR es absoluto o relativo
cat > run_oda_lite.sh <<'EOS'
#!/usr/bin/env bash
set -euo pipefail

# Calculo de ruta del script (compatible POSIX usando $0)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

DIST_DIR_INPUT="__DIST_DIR__"
CONF_DIR_OVERRIDE=""
BUILT_TELEGRAF_VERSION="__TELEGRAF_VERSION__"

# Ayuda
usage() {
  cat <<HELP
Uso: ./run_oda_lite.sh [--config-dir <ruta_conf>] [args_telegraf...]

Opciones:
  --config-dir <ruta>   Directorio con ficheros .conf (por defecto: plugins_conf dentro de --dist-dir)
  -h, --help            Muestra esta ayuda y sale

Info:
  Versión Telegraf (build): ${BUILT_TELEGRAF_VERSION}
  Binario Telegraf: ${TELEGRAF_BIN}
  Config por defecto: ${DEFAULT_CONF_DIR}
  Config efectiva: ${CONF_DIR}

Todos los argumentos no reconocidos se reenvían al binario de Telegraf.
HELP
}

# Parse args: interceptar --config-dir y reconstruir parametros restantes de forma segura
NEW_ARGS=()
SHOW_HELP=0
while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help)
      SHOW_HELP=1; shift 1 ;;
    --config-dir)
      CONF_DIR_OVERRIDE="$2"; shift 2 ;;
    --config-dir=*)
      CONF_DIR_OVERRIDE="${1#*=}"; shift 1 ;;
    *)
      NEW_ARGS+=("$1"); shift 1 ;;
  esac
done

if [[ "${DIST_DIR_INPUT}" = /* ]]; then
  TELEGRAF_BIN="${DIST_DIR_INPUT}/telegraf"
  DEFAULT_CONF_DIR="${DIST_DIR_INPUT}/plugins_conf"
else
  TELEGRAF_BIN="${SCRIPT_DIR}/${DIST_DIR_INPUT}/telegraf"
  DEFAULT_CONF_DIR="${SCRIPT_DIR}/${DIST_DIR_INPUT}/plugins_conf"
fi

if [ ! -x "${TELEGRAF_BIN}" ]; then
  echo "❌ Telegraf no encontrado en ${TELEGRAF_BIN}"
  exit 1
fi

CONF_DIR="${CONF_DIR_OVERRIDE:-${DEFAULT_CONF_DIR}}"
# Si la ruta es relativa, hacerla relativa al SCRIPT_DIR
if [[ ! "${CONF_DIR}" = /* ]]; then
  CONF_DIR="${SCRIPT_DIR}/${CONF_DIR}"
fi

# Si se solicitó ayuda, mostrar con información computada
if [ "$SHOW_HELP" -eq 1 ]; then
  usage
  # Si es posible, también mostrar versión runtime
  if "${TELEGRAF_BIN}" --version >/dev/null 2>&1; then
    echo "Runtime telegraf --version: $(${TELEGRAF_BIN} --version)"
  fi
  exit 0
fi

if [ ! -d "${CONF_DIR}" ]; then
  echo "❌ Directorio de configuración no encontrado: ${CONF_DIR}"
  exit 1
fi
shopt -s nullglob
conf_files=("${CONF_DIR}"/*.conf)
shopt -u nullglob
if [ ${#conf_files[@]} -eq 0 ]; then
  echo "❌ No se encontraron archivos .conf en ${CONF_DIR}"
  exit 1
fi

echo "🚀 Lanzando Telegraf con configs en: ${CONF_DIR}"
if [ ${#NEW_ARGS[@]} -eq 0 ]; then
  exec "${TELEGRAF_BIN}" --config-directory "${CONF_DIR}"
else
  exec "${TELEGRAF_BIN}" --config-directory "${CONF_DIR}" "${NEW_ARGS[@]}"
fi
EOS

# Sustituir placeholder por el valor real de DIST_DIR sin usar sed -i para portabilidad
tmp_run="run_oda_lite.sh.tmp"
sed -e "s|__DIST_DIR__|$DIST_DIR|g" -e "s|__TELEGRAF_VERSION__|$TELEGRAF_VERSION|g" run_oda_lite.sh > "$tmp_run" && mv "$tmp_run" run_oda_lite.sh
chmod +x run_oda_lite.sh

# Eliminar directorio de compilación para no depender de él en runtime
if [ "$KEEP_SOURCE" = true ]; then
  echo "ℹ️ Manteniendo directorio de compilación: $CLONE_DIR (flag --keep-source)"
else
  echo "🧹 Eliminando directorio de compilación: $CLONE_DIR"
  rm -rf "$CLONE_DIR"
fi

echo "✅ Compilación finalizada. Ejecutable y configs listos en: $DIST_DIR"
echo "   Usa ./run_oda_lite.sh para arrancar Telegraf"
