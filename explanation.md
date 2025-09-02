# Explicación técnica: CI/CD de Telegraf custom

Este documento explica el flujo de extremo a extremo para construir y publicar una distribución personalizada de Telegraf desde este repositorio, usando GitHub Actions. La publicación se realiza creando la Release de GitHub y subiendo los artefactos generados por el Makefile de Telegraf; no se compila dentro de la fase de publicación.

## Visión general

- Objetivo: compilar Telegraf (desde `influxdata/telegraf`) con plugins propios bajo `plugins/` y empaquetarlo junto a configuraciones `.conf` bajo `config/`.
- Modos de build:
  - `mini`: incluye todos los plugins de Telegraf + los custom.
  - `nano`: incluye solo los plugins referenciados en los `.conf` (resuelve dependencias desde `plugins_conf`).
- Outputs: archivos `tar.gz` por plataforma con el binario `telegraf` y la carpeta `plugins_conf/` con los `.conf` (en Windows se genera `.zip`). Se genera además un `.sha256` por cada artefacto.

## Flujo end-to-end

1) GitHub Action dispara el build (manual o por tag).
2) El job de build ejecuta `./cicd.sh build` (wrapper que llama a `build.sh`) para preparar `telegraf_src` con plugins y deps.
3) `build.sh` clona Telegraf, incorpora plugins, ajusta dependencias Go y compila usando `custom_builder` de Telegraf. Si no existe el directorio de `config`, fuerza el modo `mini` (si pediste `nano`, avisa y lo ignora).
4) El workflow ejecuta `make package` dentro de `telegraf_src` y copia todos los paquetes a `out/`.
5) Para releases por tag, un segundo job descarga los artifacts y crea la Release en GitHub adjuntando todos los paquetes.

## Archivos clave

- `build.sh`: script principal de compilación.
  - Flags principales: `--version`, `--mode`, `--config-dir`, `--plugins-dir`, `--dist-dir`.
  - CI-friendly: `--no-interactive` (omite prompts), `--go-get` (lista de módulos `mod@ver` separados por espacio/coma), `--go-get-file` (una dependencia por línea).
- Pasos: clona `influxdata/telegraf@v<versión>`, crea `plugins_conf/` y copia `.conf` si existen (no falla si no hay configs), copia plugins custom a `plugins/`, añade deps (`go get`), `go mod tidy`, `make build_tools`, ejecuta `tools/custom_builder/custom_builder` según `mode` (forzando `mini` si no hay `config/`), copia `telegraf` y los `.conf` al `--dist-dir` y limpia el árbol clonado.
  - Copia de configs a destino: si `--config-dir` apunta al mismo directorio que el destino (`<dist>/plugins_conf`), no se borra ni se recopia para evitar perder los `.conf` del usuario.
  - Validaciones: ya no falla si no existen configs durante el build; la validación de configs se hace en ejecución.

- `cicd.sh`: wrapper de CI/CD.
  - Comando: `build`.
  - Prepara `telegraf_src` (clonado de Telegraf + plugins + deps) y deja binario y configs en `--dist-dir`.
  - No empaqueta ni publica; el empaquetado se hace con `make package` en los workflows y la publicación con `softprops/action-gh-release`.

- `.github/workflows/build-telegraf.yml`: workflow manual (workflow_dispatch).
  - Inputs: versión, modo, rutas de `config/` y `plugins/`, `dist_dir` (opcional), `go_get`, `go_get_file`, `go_version`.
  - `dist_dir`: si se omite, `cicd.sh` usará `dist` por defecto y lo creará antes del build. Si se define en el input, se crea ese directorio y se pasa a `build.sh`.
  - Validación de configs: solo se exige que `config_dir` exista y tenga `.conf` cuando `mode` = `nano`.
  - Matrix: `linux/amd64` y `linux/arm64`.
  - Ejecuta `./cicd.sh build ... --keep-source`, instala `fpm/rpm/zip` y luego `make package` en `telegraf_src`; copia `build/dist/*` a `out/` y los sube como artifacts.

- `.github/workflows/release.yml`: workflow por tag.
  - Trigger: `push` de tags `v*` o `custom-telegraf-*`.
  - Job build: prepara `telegraf_src` y ejecuta `make package` segmentado por grupos usando `include_packages` (Linux core/legacy, Windows, Darwin, FreeBSD) para reducir uso de disco por runner.
    - Nota: el grupo Linux "alt" (mips, mipsel, riscv64, loong64, s390x, ppc64le) está comentado temporalmente para ahorrar espacio/tiempo; puede reactivarse editando la matriz del workflow.
    - Paso previo libera espacio del runner (`/usr/share/dotnet`, `/opt/ghc`, Android, CodeQL, prune de Docker) y al final limpia caches (`go clean`) y `telegraf_src/build`.
  - Job `release`: descarga artifacts y crea la Release subiendo todos los assets.
  - Checkout con `fetch-depth: 0` para correcta detección de tags previos.

Nota: ya no usamos GoReleaser para publicar; la Release se crea con la acción `softprops/action-gh-release` adjuntando todos los artifacts generados por los jobs de build.

## Empaquetado (.deb/.rpm)

- Tras el build, el workflow ejecuta dentro de `telegraf_src` los targets del Makefile de Telegraf:
  - `make package-deb` y `make package-rpm` (si no existe el target específico, usa `make package`).
- Los paquetes resultantes se recogen desde `telegraf_src/dist/` y se suben como artifacts del job.
- En el job de release, GoReleaser adjunta esos `.deb`/`.rpm` como assets del tag (vía `release.extra_files`).

## Detalles técnicos relevantes

- Clonado de Telegraf: `git clone --branch v<versión> --depth 1 https://github.com/influxdata/telegraf.git` en `telegraf_src`.
- Plugins custom: este repo espera implementación en `plugins/<tipo>/<nombre>/...` (ej. `plugins/outputs/og_report`). Se copian al árbol de Telegraf antes de compilar.
- Dependencias Go extra: si un plugin necesita módulos no presentes en `telegraf/go.mod`, pásalos con `--go-get "mod@ver, otro@latest"` o `--go-get-file deps.txt`.
  - `--go-get-file` acepta ruta relativa o absoluta; las relativas se resuelven respecto a la raíz del repo (no al directorio temporal de Telegraf).
- Modo `mini`: genera `plugins_conf/telegraf_all.conf` con stanzas para todos los plugins (excepto una lista de excluidos básicos) y usa `custom_builder` para resolverlos.
- Cross-compilación: el workflow define `GOOS` y `GOARCH` en cada job (matrix). Se usa `CGO_ENABLED=0` para evitar dependencias nativas (revisa esto si añades plugins que requieren Cgo).

## Disparadores y publicación

- Manual: Actions → “Build Custom Telegraf”. Permite probar builds sin publicar Release.
- Por tag: al hacer push de una etiqueta `vX.Y.Z` o `custom-telegraf-...`, se compila y se publica Release automáticamente subiendo los artefactos al tag.

## Permisos, tokens y configuración del repo

Requisitos en GitHub (Settings del repo):

- Actions → General → Allow GitHub Actions: habilitado.
- Actions → General → Workflow permissions: marcar “Read and write permissions” o, alternativamente, dejamos `permissions: contents: write` en los jobs que lo necesiten (ya está en los workflows).
- Actions → General → Allow actions and reusable workflows: si tu organización restringe, permitir específicamente:
  - `actions/checkout@v4`
  - `actions/setup-go@v5`
  - `actions/upload-artifact@v4`
  - `actions/download-artifact@v4`
  - `goreleaser/goreleaser-action@v6`
- Secrets:
  - No necesitas crear `GITHUB_TOKEN`; GitHub lo inyecta automáticamente en los workflows. Debe tener permisos de `contents: write` (ya indicado en el job `release`).
  - Si ejecutas `./cicd.sh build-and-release` fuera de Actions, debes exportar `GITHUB_TOKEN` con un token personal con `repo` scope.

## Cómo extender

- Más plataformas: añade a la matrix (ej. `linux/arm/v7`, `linux/386`, `darwin/amd64`, `darwin/arm64`). Valida compatibilidad de plugins; algunos podrían requerir `CGO_ENABLED=1` o toolchains.
- Inputs adicionales: ya se expone `--go-get-file`; también puedes exponer `--dist-dir` como input si quieres variarlo desde UI.
- Versionado: si los tags siguen SemVer (`vX.Y.Z`), las Releases quedarán alineadas con la versión de Telegraf o tu variante.

## Solución de problemas

- “No .conf files found”: en modo `nano`, asegúrate de tener `config/*.conf` en el repo (el workflow lo valida). En `mini`, no es obligatorio.
- Errores `go get`: usa `--go-get`/`--go-get-file` y fija versiones (`mod@vX.Y.Z`).
- Plugins con Cgo: podrían fallar con `CGO_ENABLED=0`. Cambia a `CGO_ENABLED=1` y provee toolchain adecuada en el runner.
- Permisos insuficientes al publicar: revisa “Workflow permissions” y que el job de release tenga `permissions: contents: write` (incluido).
- Restricción de acciones de terceros: si tu organización las bloquea, explícitamente permítelas o crea mirrors internos.

---

Última actualización: generada junto con la integración inicial de CI/CD (workflows, cicd.sh y GoReleaser). Este archivo se actualizará con cambios futuros.
## Ejecución local del binario empaquetado

Se genera un `run_oda_lite.sh` que arranca Telegraf con un directorio de configuración validado:

- Uso: `./run_oda_lite.sh [--config-dir <ruta_conf>] [args_telegraf...]`
- Por defecto, usa `plugins_conf` dentro de `--dist-dir` definido al construir.
- Validación: comprueba que el directorio exista y contenga al menos un `.conf`. Si no, aborta con mensaje claro.
- Ayuda: `./run_oda_lite.sh --help` muestra opciones y uso.
   - Incluye versión de Telegraf del build, binario detectado y rutas de configuración por defecto y efectiva.

Ejemplos prácticos:
- `./run_oda_lite.sh` → usa `plugins_conf` del paquete.
- `./run_oda_lite.sh --config-dir /etc/telegraf/custom` → usa configs externas.
- `./run_oda_lite.sh --config-dir ./plugins_conf --test --once` → reenvía flags de Telegraf.
