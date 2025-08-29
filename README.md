# oda-lite
OpenGate Device Agente Lite Version is based in Telegraf

## CI/CD

Este repo incluye un workflow de GitHub Actions para construir una distribución custom de Telegraf con los plugins bajo `plugins` y las configuraciones `.conf` bajo `config`.

- Workflow: `.github/workflows/build-telegraf.yml`
- Dispara desde la pestaña Actions (workflow_dispatch).
- Inputs principales:
  - `telegraf_version` (por defecto `1.31.1`)
  - `mode` (`mini` o `nano`)
  - `config_dir` (directorio con ficheros `.conf`)
  - `plugins_dir` (directorio con plugins custom)
  - `dist_dir` (directorio de salida)
  - `go_get` (opcional: dependencias extra para `go get`)
  - `go_get_file` (opcional: ruta a fichero con un módulo por línea)
  - Compila en matrix (`linux/amd64` y `linux/arm64`).

Nota: `config_dir` solo es obligatorio en modo `nano`. En `mini`, si falta, el build continúa y se fuerza `mini` desde `build.sh`.

Salida: un `tar.gz` por plataforma con el binario `telegraf` y los `.conf` en `plugins_conf/`.

### Ejecutar el binario empaquetado

Se genera un script de ayuda `run_oda_lite.sh` junto al artefacto compilado. Ejemplos:

- Usar configs por defecto empaquetadas:
  - `./run_oda_lite.sh`

- Especificar un directorio de configuración alternativo:
  - `./run_oda_lite.sh --config-dir /ruta/a/mis/configs`

- Pasar argumentos adicionales a Telegraf (se reenvían tal cual):
  - `./run_oda_lite.sh --config-dir ./plugins_conf --test --once`

### Script de CI/CD

El script `cicd.sh` envuelve a `build.sh` para uso en CI/CD:

- Build: `./cicd.sh build --version 1.31.1 --mode mini --config-dir config --plugins-dir plugins --dist-dir dist --artifact-dir out`
- Dependencias extra: añadir `--go-get "mod1@ver, mod2@latest"` o `--go-get-file deps.txt`.
- Publicación (manual/local): `GITHUB_TOKEN=... ./cicd.sh build-and-release --version 1.31.1 --mode mini --publish [--release-tag v1.31.1-custom]`.

### Releases por tag con GoReleaser

Workflow: `.github/workflows/release.yml`

- Trigger: push de tags `v*` o `custom-telegraf-*`.
- Job matrix construye artefactos y checksums y los sube como artifacts.
- Job de release descarga los artifacts y usa GoReleaser (`.goreleaser.yaml`) para crear la Release y adjuntar los tarballs/checksums como blobs.
- Memoria del proyecto: `.codex/memory.json` y util `scripts/mem.sh` para añadir/listar/olvidar entradas (decisiones, TODOs, convenciones). Ejemplos:
  - `./scripts/mem.sh remember "Usar GoReleaser con blobs" --type decision --tags goreleaser,actions`
  - `./scripts/mem.sh list --scope repo`
  - `./scripts/mem.sh list --scope local`
