# Explicación técnica: CI/CD de Telegraf custom

Este documento explica el flujo de extremo a extremo para construir y publicar una distribución personalizada de Telegraf desde este repositorio, usando GitHub Actions y GoReleaser. Se mantendrá actualizado a medida que evolucione el pipeline.

## Visión general

- Objetivo: compilar Telegraf (desde `influxdata/telegraf`) con plugins propios bajo `plugins/` y empaquetarlo junto a configuraciones `.conf` bajo `config/`.
- Modos de build:
  - `mini`: incluye todos los plugins de Telegraf + los custom.
  - `nano`: incluye solo los plugins referenciados en los `.conf` (resuelve dependencias desde `plugins_conf`).
- Outputs: archivos `tar.gz` por plataforma con el binario `telegraf` y la carpeta `plugins_conf/` con los `.conf`. Se genera además un `.sha256` por cada `tar.gz`.

## Flujo end-to-end

1) GitHub Action dispara el build (manual o por tag).
2) El job de build ejecuta `./cicd.sh build` (wrapper que llama a `build.sh`).
3) `build.sh` clona Telegraf, incorpora plugins, ajusta dependencias Go y compila usando `custom_builder` de Telegraf. Si no existe el directorio de `config`, fuerza el modo `mini` (si pediste `nano`, avisa y lo ignora).
4) `cicd.sh` empaqueta resultados en `out/*.tar.gz` y genera `*.sha256`.
5) Para releases por tag, un segundo job descarga los artifacts en `dist/` y ejecuta GoReleaser, que crea/actualiza la Release y adjunta los `tar.gz`/`sha256` usando `release.extra_files`.

## Archivos clave

- `build.sh`: script principal de compilación.
  - Flags principales: `--version`, `--mode`, `--config-dir`, `--plugins-dir`, `--dist-dir`.
  - CI-friendly: `--no-interactive` (omite prompts), `--go-get` (lista de módulos `mod@ver` separados por espacio/coma), `--go-get-file` (una dependencia por línea).
- Pasos: clona `influxdata/telegraf@v<versión>`, crea `plugins_conf/` y copia `.conf` si existen (no falla si no hay configs), copia plugins custom a `plugins/`, añade deps (`go get`), `go mod tidy`, `make build_tools`, ejecuta `tools/custom_builder/custom_builder` según `mode` (forzando `mini` si no hay `config/`), copia `telegraf` y los `.conf` al `--dist-dir` y limpia el árbol clonado.
  - Copia de configs a destino: si `--config-dir` apunta al mismo directorio que el destino (`<dist>/plugins_conf`), no se borra ni se recopia para evitar perder los `.conf` del usuario.
  - Validaciones: ya no falla si no existen configs durante el build; la validación de configs se hace en ejecución.

- `cicd.sh`: wrapper de CI/CD.
  - Subcomandos: `build`, `build-and-release`.
  - Construye con `build.sh`, empaqueta en `out/telegraf-custom-<ver>-<mode>-<GOOS>-<GOARCH>.tar.gz` y genera `*.sha256`.
  - `--publish` (opcional) publica Release vía API (requiere `GITHUB_TOKEN`). El workflow de tags usa GoReleaser en su lugar.

- `.github/workflows/build-telegraf.yml`: workflow manual (workflow_dispatch).
  - Inputs: versión, modo, rutas de `config/` y `plugins/`, `dist_dir` (opcional), `go_get`, `go_get_file`, `go_version`.
  - `dist_dir`: si se omite, el wrapper `cicd.sh` usará `dist` por defecto y lo creará antes del build. Si se define en el input, se crea ese directorio y se pasa a `build.sh`.
  - Validación de configs: solo se exige que `config_dir` exista y tenga `.conf` cuando `mode` = `nano`.
  - Matrix: `linux/amd64` y `linux/arm64` (`GOOS`/`GOARCH`, `CGO_ENABLED=0`).
  - Ejecuta `./cicd.sh build ...` y sube artifacts `tar.gz` y `sha256`.

- `.github/workflows/release.yml`: workflow por tag.
  - Trigger: `push` de tags `v*` o `custom-telegraf-*`.
  - Job matrix: igual que el manual, genera artifacts y los sube.
  - Job `release`: descarga artifacts a `dist/` y ejecuta GoReleaser para crear/actualizar la Release y adjuntar los assets desde `dist/**`.
  - Checkout con `fetch-depth: 0` para correcta detección de tags previos.

- `.goreleaser.yaml`: configuración de GoReleaser (v2).
  - No compila binarios (sin `builds`); adjunta archivos ya generados mediante `release.extra_files`.
  - Busca archivos bajo `dist/**/*.tar.gz` y `dist/**/*.sha256` (el job de release descarga artifacts ahí para evitar estado "dirty").

## Detalles técnicos relevantes

- Clonado de Telegraf: `git clone --branch v<versión> --depth 1 https://github.com/influxdata/telegraf.git` en `telegraf_src`.
- Plugins custom: este repo espera implementación en `plugins/<tipo>/<nombre>/...` (ej. `plugins/outputs/og_report`). Se copian al árbol de Telegraf antes de compilar.
- Dependencias Go extra: si un plugin necesita módulos no presentes en `telegraf/go.mod`, pásalos con `--go-get "mod@ver, otro@latest"` o `--go-get-file deps.txt`.
  - `--go-get-file` acepta ruta relativa o absoluta; las relativas se resuelven respecto a la raíz del repo (no al directorio temporal de Telegraf).
- Modo `mini`: genera `plugins_conf/telegraf_all.conf` con stanzas para todos los plugins (excepto una lista de excluidos básicos) y usa `custom_builder` para resolverlos.
- Cross-compilación: el workflow define `GOOS` y `GOARCH` en cada job (matrix). Se usa `CGO_ENABLED=0` para evitar dependencias nativas (revisa esto si añades plugins que requieren Cgo).

## Disparadores y publicación

- Manual: Actions → “Build Custom Telegraf”. Permite probar builds sin publicar Release.
- Por tag: al hacer push de una etiqueta `vX.Y.Z` o `custom-telegraf-...`, se compila y se publica Release automáticamente con GoReleaser.
  - GoReleaser detecta la versión desde el tag actual del commit.
  - Publica los tarballs y checksums como assets en la Release usando `release.extra_files` (acción fijada a `v2.11.2`).

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
