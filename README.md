# Free Sync

Goal: keep **two FileMaker files** (“blue” / “green”) **mirrored**—same logical data on both sides—so either copy can act as **hot standby** or **failover** when traffic fails over between them.

Bidirectional **OData** sync runs in one process. Changes are driven by `ModificationTimestamp` and a small JSON checkpoint—not full-table pulls.

## Requirements

- Go **1.22+**

## Build & run

```bash
make build   # or: go build -o freesync ./cmd/freesync
./freesync run
```

Writes are **dry-run** unless you pass **`-apply`**.

```bash
./freesync run -apply
```

Paths:

```bash
go run ./cmd/freesync run -apply
```

## Config

- **`config/dev.example.json`** — template in repo; URLs are placeholders.
- **`config/dev.local.json`** — real hosts/creds (gitignored). Copy from the example and edit.

- Performance knobs: `batchSize` (default `50`), `maxWorkers` (default `8`), `verifyMode` (`off` default, `strict` for full post-apply verification).

Environment (optional; overrides defaults):

| Variable | Default |
|----------|---------|
| `FREESYNC_CONFIG` | `config/dev.example.json` |
| `FREESYNC_STATE` | `data/sync-state.json` |

Flags **`-config`** and **`-state`** set the same paths.

Flag order is flexible: **`freesync run -apply`** and **`freesync -config ./cfg.json run`** both work.

## What gets synced

- Tables are listed under **`tables`** in config; each needs **`name`**, **`primaryKey`**, **`modifiedField`** (or use **`defaults`**).
- **Schema:** intersection of fields on both files. **`$metadata`** is used to skip **calculated** / **summary** / OData **Computed** properties when those annotations are present. Use per-table **`fieldOverrides`** to include a field that would otherwise be skipped.
- Details and FileMaker OData quirks (query encoding, etc.): **`docs/FILEMAKER_ODATA.md`**.

## Tests

```bash
go test ./... -count=1
```

Optional live smoke test against **`config/dev.local.json`** (needs network + valid OData):

```bash
FREESYNC_LIVE=1 go test ./internal/odata -run TestLive -count=1 -v
```

## More

| Doc | Contents |
|-----|----------|
| **`SPEC.md`** | Product behavior, checkpoint semantics |
| **`docs/DEVELOPMENT.md`** | Layout, dev workflow |
