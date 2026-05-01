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
FREESYNC_CONFIG=config/dev.local.json go run ./cmd/freesync run
# writes (default is dry-run until you pass -apply):
FREESYNC_CONFIG=config/dev.local.json go run ./cmd/freesync run -apply
```

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
