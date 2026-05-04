# Free Sync

Goal: keep **two FileMaker files** (“blue” / “green”) **mirrored**—same logical data on both sides—so either copy can act as **hot standby** or **failover** when traffic fails over between them.

Bidirectional **OData** sync runs in one process. Changes are driven by `ModificationTimestamp` and a small JSON checkpoint—not full-table pulls.

## Getting Started

Requirements:

- Go **1.22+**
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
freesync run [-apply] [-config path] [-state path]
```

Examples:

```bash
freesync run -apply
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

Flags **`-config`** and **`-state`** set the same paths.

## Sync Rules

- Tables are listed under **`tables`** in config; each needs **`name`**, **`primaryKey`**, **`modifiedField`** (or use **`defaults`**).
- **Schema:** intersection of fields on both files. **`$metadata`** is used to skip **calculated** / **summary** / OData **Computed** properties when those annotations are present. Use per-table **`fieldOverrides`** to include a field that would otherwise be skipped.
- On first run without checkpoint: `bootstrapMode=binary` uses lightweight head probes and binary search to find a narrow bootstrap window; if probe fails, it falls back to fixed `initialLookback`.

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
