# Development

## Requirements

- **Go** 1.22+ ([install](https://go.dev/dl/))

## TDD loop

```bash
go test ./... -count=1
FREESYNC_LIVE=1 go test ./internal/odata -run TestLive -count=1 -v
```

Run tests for one package:

```bash
go test ./internal/domain/ -v
```

## Build CLI

```bash
make build
./freesync run
```

## Run CLI

```bash
go run ./cmd/freesync run
go run ./cmd/freesync run -config config/dev.local.json
# writes (default is dry-run until you pass -apply):
go run ./cmd/freesync run -config config/dev.local.json -apply
```

Config note:

- `config/dev.local.json` now uses a top-level `files[]` array.
- Runtime settings such as listen/token/state path now live under top-level `runtime`.
- Each file entry defines its own `blue`/`green` OData URLs.
- `files[].tables` is optional; omitting it auto-discovers the blue/green base-table intersection via `FileMaker_BaseTables`.
- If `files[].tables` is present, that explicit list takes precedence over auto-discovery.
- `data/sync-state.json` stores per-file checkpoints in one JSON file.

## Layout

| Path | Role |
|------|------|
| `cmd/freesync` | CLI (`run`, `-apply`) |
| `internal/config` | JSON config |
| `internal/domain` | Window, LWW, `BuildPlan` |
| `internal/odata` | HTTP OData client, manifest fetch |
| `internal/run` | One-shot orchestration (verify + checkpoint) |
| `internal/state` | Checkpoint JSON (`data/sync-state.sqlite` per SPEC later) |
| `internal/timespec` | `1d` / `90d` duration strings |
| `testdata/` | Invalid config samples for tests |
