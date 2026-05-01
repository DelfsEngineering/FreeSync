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

## Layout

| Path | Role |
|------|------|
| `cmd/freesync` | CLI |
| `internal/domain` | Pure sync logic (tests first, no FM network) |
| (later) `internal/odata`, `internal/state` | Adapters |
