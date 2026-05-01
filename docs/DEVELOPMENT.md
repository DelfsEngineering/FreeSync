# Development

## Requirements

- **Go** 1.22+ ([install](https://go.dev/dl/))

## TDD loop

```bash
go test ./... -count=1
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

## Run CLI (loads config, prints servers; full OData sync not wired)

```bash
go run ./cmd/freesync run
FREESYNC_CONFIG=config/dev.local.json go run ./cmd/freesync run
```

## Layout

| Path | Role |
|------|------|
| `cmd/freesync` | CLI (`run`) |
| `internal/config` | JSON config |
| `internal/domain` | Window, LWW, `BuildPlan` (tests) |
| `internal/state` | Checkpoint file (`data/sync-state.json`); SQLite per SPEC later |
| `testdata/` | Invalid config samples for tests |
