# Free Sync

Goal: keep **two FileMaker files** (“blue” / “green”) **mirrored**—same logical data on both sides—so either copy can act as **hot standby** or **failover** when traffic fails over between them.

Bidirectional **OData** sync runs in one process. Changes are driven by `ModificationTimestamp` and a small JSON checkpoint—not full-table pulls.

## Features

- Bidirectional or one-way FileMaker OData sync (`to-blue` / `to-green`).
- Multithreaded apply workers with bounded concurrency, plus parallel blue/green manifest and schema reads.
- Automatic sync-field filtering from FileMaker schema: skips calculated, summary, missing, and explicitly ignored local-generated fields.
- Stable keyset pagination over modification windows, avoiding offset drift during live writes.
- Safe checkpoint windows with configurable overlap so repeated runs only recheck recent changes.
- Per-record retry/defer handling for transient FileMaker locks, network issues, and malformed record reads.
- Docker/Kubernetes-ready HTTP service with bearer-token trigger endpoint and concise operational logs.

## Getting Started

Requirements:

- **Docker** for the preferred run/deploy path. The image build includes Go and produces the `freesync` binary.
- Go **1.22+** only if you want to run tests or build from source without Docker.
- A valid FileMaker OData config (see setup below)

1) Copy and edit config:

```bash
cp config/dev.example.json config/dev.local.json
```

Fill in:

- blue + green OData URLs
- usernames/passwords
- table list and PK/modified field names

2) Build:

```bash
make build
```

3) Run a dry run:

```bash
./freesync run -config config/dev.local.json -state data/sync-state.json
```

4) Apply writes:

```bash
./freesync run -apply -config config/dev.local.json -state data/sync-state.json
```

Writes are **dry-run by default** unless `-apply` is present.

## CLI Usage

Run once:

```bash
freesync run [-apply] [-one-way to-blue|to-green] [-config path] [-state path] [-verbose]
```

Examples:

```bash
freesync run -apply
freesync run -apply -one-way to-blue
freesync run -apply -one-way to-blue -verbose
freesync -config ./config/dev.local.json run -apply
go run ./cmd/freesync run -apply
```

## HTTP Trigger Mode (for k8s / API calls)

Run as a service:

```bash
./freesync serve -listen :8080 -config config/dev.local.json -state data/sync-state.json -apply
```

Endpoints:

- `GET /healthz`
- `POST /run` (runs one sync pass, JSON response)

Token auth (recommended):

```bash
FREESYNC_TRIGGER_TOKEN=supersecret ./freesync serve -config config/dev.local.json
curl -X POST http://localhost:8080/run \
  -H "Authorization: Bearer supersecret"
```

- `POST /run?apply=false` forces dry-run for that request.
- `POST /run?oneWay=to-blue` forces one-way updates to blue only for that request.
- `POST /run?verbose=true` includes page-level manifest/debug logs for that request.
- If a run is already in progress, another `POST /run` returns `409`.

## Configuration

- **`config/dev.example.json`** — template in repo; URLs are placeholders.
- **`config/dev.local.json`** — real hosts/creds (gitignored). Copy from the example and edit.

- Performance knobs: `batchSize` (default `50`), `maxWorkers` (default `8`), `verifyMode` (`off` default, `strict` for full post-apply verification).
- Bootstrap behavior: `bootstrapMode` (`fixed` default, `binary` for successive approximation of divergence boundary on first run).

Environment (optional):

| Variable | Default |
|----------|---------|
| `FREESYNC_CONFIG` | `config/dev.example.json` |
| `FREESYNC_STATE` | `data/sync-state.json` |
| `FREESYNC_TRIGGER_TOKEN` | empty (no auth required if unset) |
| `FREESYNC_LISTEN` | `:8080` (serve mode) |
| `FREESYNC_ONE_WAY` | empty (bidirectional) |
| `FREESYNC_VERBOSE` | `false` |

Flags **`-config`** and **`-state`** set the same paths.

## Sync Rules

- Tables are listed under **`tables`** in config; each needs **`name`**, **`primaryKey`**, **`modifiedField`** (or use **`defaults`**).
- **Schema:** intersection of fields on both files. Free Sync prefers the thin FileMaker `FileMaker_Fields` system table to skip calculated/summary fields; full **`$metadata`** remains a fallback. Use per-table **`fieldOverrides`** to include a field that would otherwise be skipped.
- Use per-table **`ignoreFields`** for FileMaker-local generated fields (for example auto-enter URLs or cache/version fields). Ignored fields are excluded from PATCH bodies and strict verification, even if OData reports them as normal writable fields.
- On first run without checkpoint: `bootstrapMode=binary` uses lightweight head probes and binary search to find a narrow bootstrap window; if probe fails, it falls back to fixed `initialLookback`.

Example table entry:

```json
{
  "name": "Forms",
  "primaryKey": "id",
  "modifiedField": "ModificationTimestamp",
  "ignoreFields": ["thumbURL"]
}
```

## Tests

```bash
go test ./... -count=1
```

Container smoke test (build + run + health/auth/trigger checks):

```bash
make test-container
```

Notes:

- `make test-container` expects Docker daemon access (Docker Desktop or Colima).
- It mounts `config/dev.local.json` and `data/` from your local repo.

## Docker

```bash
docker build -t freesync:latest .
docker run --rm -p 8080:8080 \
  -e FREESYNC_CONFIG=/app/config/dev.local.json \
  -e FREESYNC_STATE=/app/data/sync-state.json \
  -e FREESYNC_TRIGGER_TOKEN=supersecret \
  -v "$(pwd)/config:/app/config:ro" \
  -v "$(pwd)/data:/app/data" \
  freesync:latest
```

## Kubernetes Quick Setup

1) Build and push image:

```bash
docker build -t your-registry/freesync:latest .
docker push your-registry/freesync:latest
```

2) Deploy one replica with:

- config mounted at `/app/config/dev.local.json`
- persistent state mounted at `/app/data/sync-state.json`
- `FREESYNC_TRIGGER_TOKEN` from secret
- args: `["serve","-listen",":8080","-apply"]`

3) Trigger sync:

```bash
curl -X POST http://<service>:8080/run \
  -H "Authorization: Bearer <token>"
```

Optional live OData smoke test (requires valid network + credentials):

```bash
FREESYNC_LIVE=1 go test ./internal/odata -run TestLive -count=1 -v
```

## More

| Doc | Contents |
|-----|----------|
| **`SPEC.md`** | Product behavior, checkpoint semantics |
| **`docs/DEVELOPMENT.md`** | Layout, dev workflow |
| **`docs/FILEMAKER_ODATA.md`** | FileMaker OData quirks and API notes |
| **`docs/DOCKER_K8S.md`** | Container + k8s deployment examples |
