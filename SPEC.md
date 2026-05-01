# Free Sync — Specification

## System Name

**Free Sync** — OData bidirectional sync between two FileMaker servers. Ship artifacts may use names such as `freesync` / `freesync:latest` for CLI or Docker image tags.

## Purpose

Bidirectional synchronization between two FileMaker servers over OData, supporting:

- blue/green deployments
- warm standby servers
- manual flip with near-zero data drift
- large datasets (thousands+ records)
- minimal infrastructure (single container)

## Core Principles

- No primary/secondary (fully bidirectional)
- Last-write-wins using ModificationTimestamp
- Sync only what changed (no full table pulls)
- Prove correctness before advancing state
- Stateless container, state in small SQLite DB
- Works even after long offline periods

## High-Level Architecture

**Docker Container (Go binary)**

**Inputs:**

- JSON config (env or file)
- OData endpoints (Server A + B)

**State:**

- SQLite (`/data/sync-state.sqlite`)

**Flow:**

1. Validate config + schema
2. Determine sync window
3. Fetch lightweight manifests
4. Compare
5. Fetch full records (only diffs)
6. Apply upserts/deletes
7. Verify
8. Advance checkpoint

## Config

```json
{
  "servers": [
    {
      "id": "blue",
      "url": "https://blue/fmi/odata/v4/db",
      "username": "...",
      "password": "..."
    },
    {
      "id": "green",
      "url": "https://green/fmi/odata/v4/db",
      "username": "...",
      "password": "..."
    }
  ],
  "tables": [
    {
      "name": "Contacts",
      "primaryKey": "id",
      "modifiedField": "ModificationTimestamp"
    }
  ],
  "defaults": {
    "primaryKey": "id",
    "modifiedField": "ModificationTimestamp"
  },
  "overlapMinutes": 10,
  "initialLookback": "1d",
  "maxLookback": "90d",
  "schemaMode": "intersection"
}
```

## Data Requirements (FileMaker)

### Required per table

- Primary Key (default: `id`)
- ModificationTimestamp (auto-updated on create/edit)

### Required delete journal

**SyncDeletes**

- `id`
- `tableName`
- `recordId`
- `deletedAt`
- `deletedOnServerId`

## Sync Model

### Detection

- If id exists both sides: compare ModificationTimestamp
- If id exists only one side: create on other side
- If delete exists: treat as write with timestamp

### Conflict Resolution

Latest timestamp wins:

- edit vs edit → newer wins
- delete vs edit → newer wins

## Core Algorithm

### Step 1: Load checkpoint

`safeThroughTimestamp` (per table)

If missing:

- use `seedCloneTimestamp` OR
- use fallback lookback OR
- start bootstrap mode

### Step 2: Define window

- `windowStart = safeThroughTimestamp - overlap`
- `windowEnd = now`

### Step 3: Fetch manifests (paged)

Only fetch:

- `id`
- ModificationTimestamp

From both servers.

### Step 4: Compare manifests

Detect:

- missing records
- newer records
- delete entries

### Step 5: Hydrate differences

Fetch full records ONLY for:

- changed ids
- missing ids

### Step 6: Apply changes

- upsert
- delete

### Step 7: VERIFY (critical)

Re-fetch manifests for same window.

- if mismatch → retry/fail
- if match → advance checkpoint

### Step 8: Advance checkpoint

`safeThroughTimestamp = windowEnd`

## Adaptive Windowing (Elegant Scaling)

Instead of fixed 1d/7d/30d:

`targetRowsPerWindow = 5,000`

**Behavior:**

- small window → expand next run
- large window → shrink

**Fallback expansion:**

`1d → 7d → 30d → 90d`

## Bootstrap Mode (No State)

1. Start from `fallbackLookback`
2. Process in time windows
3. Advance forward
4. Store checkpoint

No full-table memory loads.

## Schema Handling

**Mode: intersection**

- field in both → sync
- field missing on one → ignore
- missing required fields → fail

## Performance Strategy

- never fetch full tables
- page manifests (e.g. 1000 rows)
- hydrate only diffs
- stream process, no large memory sets

## SQLite State (small)

**sync_state**

- `tableName`
- `safeThroughTimestamp`

**sync_runs**

- `id`
- `status`
- `timestamps`

No record data stored.

## Execution Modes

### V1 (recommended)

```text
docker run freesync run
```

runs once, exits

### Future

**serve** mode (HTTP API)

- start sync
- cancel
- status

## Development workflow & observability (IDE / closed loop)

### Can it run “in this IDE”?

**Yes, for development.** Cursor is a normal editor with a terminal: there is no special “run inside the IDE” runtime beyond what you already use for Go or Docker.

| Approach | What you do | Notes |
|----------|-------------|--------|
| **CLI in Integrated Terminal** | Open this repo → Terminal → run the binary or `go run` with config pointing at dev OData URLs | Same machine as FileMaker not required; needs **network reachability** to blue/green dev servers (VPN, LAN, or HTTPS). |
| **Debugger** | Use the IDE’s **Run and Debug** (e.g. Go `launch.json`) to set breakpoints in orchestration, stepping through manifest → apply → verify | Best for narrowing bugs; not required for day-to-day sync validation. |
| **Docker** | `docker run` / Compose with **bind-mount** for config + `/data` SQLite | Matches production; rebuild image when code changes unless using live-mount dev image. |

FileMaker Server itself does **not** run inside Cursor; the app only **calls OData over the network**. For fully offline work, use **fakes / stub HTTP** (see TDD section) or recorded fixtures.

### Logs (what to implement so results are visible)

- **Default sink:** **stdout and/or stderr** in the dev container/process so the Integrated Terminal shows output immediately.
- **Structure:** Prefer **one line per event** (plain text with key=value, or **JSON Lines**) including at minimum: `run_id`, `table`, `phase` (`manifest` \| `compare` \| `hydrate` \| `apply` \| `verify`), `window_start`, `window_end`, `duration_ms`, `level`.
- **Human summary:** At end of a successful or failed `run`, emit a **short summary block**: tables touched, row counts (manifest rows, hydrated, upserted, deleted), whether **checkpoint advanced**, exit reason on failure.
- **Levels:** `DEBUG` (optional paging traces), `INFO` (milestones), `WARN`, `ERROR` — controlled by env e.g. `LOG_LEVEL`.

Optional later: `LOG_FILE=/path` for a rotating file on long runs; not required for V1 closed loop if terminal capture is enough.

### Results beyond logs

- **Exit codes (stable contract):** e.g. `0` = success and verified; `1` = verify mismatch or partial apply; `2` = config invalid; `3` = network/OData failure — exact mapping is an implementation detail but should be **documented** so scripts and CI can branch.
- **SQLite:** Print resolved path to `sync-state.sqlite` on startup (or in summary) so developers can inspect `sync_state` / `sync_runs` with `sqlite3` or a GUI tool.
- **`sync_runs`:** Each run should log its **row id** (or UUID) so logs and DB rows **correlate**.

### Closed-loop development (you ↔ tooling ↔ assistant)

1. **Run** a sync from the Terminal in this repo (`run` once, verbose `LOG_LEVEL=debug` when needed).
2. **Observe** structured lines + final summary + exit code.
3. **Inspect** SQLite if checkpoint or counts look wrong.
4. **Paste** terminal output (or relevant log slice) into chat for review — works best when logs are **consistent and include run/table/window context**, not only free-form prose.
5. **Tests** stay the source of truth for regressions; logs are for integration debugging and operational proof.

This keeps development **tight**: no separate dashboard required for V1; the IDE terminal + optional DB peek + tests form the loop.

## Failure Handling

- retry window on failure
- do NOT advance checkpoint unless verified
- logs per table + window

## Nice-to-Have (Later)

- bucket checksums (skip unchanged windows)
- parallel table sync
- partial table runs
- N-server support (hub model)

## What This Avoids

- CRDT complexity
- full table scans
- constant background syncing
- heavy infrastructure

## What This Guarantees

- no missed updates (overlap + verification)
- safe recovery after downtime
- minimal data transfer
- deterministic sync behavior

## Building with TDD (Test-Driven Development)

TDD fits this design if you **separate pure logic from I/O** and test **in order of dependency**: domain → orchestration → adapters → thin e2e.

### 1. Pure domain (fast, table-driven — write these first)

No network, no SQLite. Input in, struct out.

- **Window math:** `windowStart` / `windowEnd` from `safeThroughTimestamp`, `overlap`, `now` (edge cases: missing checkpoint, overlap larger than lookback, clock at boundary).
- **Manifest compare:** Given two side-manifests (id + `ModificationTimestamp`) and delete-journal events in the same window, produce the **plan**: which ids to create, update, delete on each target, and **why** (LWW: compare timestamps; delete vs edit with `deletedAt`).
- **Verify predicate:** Given pre-apply and post-apply manifest snapshots, whether “match” is satisfied (define equality rules once here).
- **Adaptive windowing:** Given row count from a trial manifest pass, how the **next** window size changes given `targetRowsPerWindow` and min/max bounds.

**TDD loop:** one behavior per test, start with the smallest example (single id, then two sides, then delete journal entry).

### 2. In-memory fakes (still no real FM)

- **OData client interface** in your app: `ListManifestPage`, `GetRecord`, `Upsert`, `Delete` (or batch variants).  
- **Fake** returns scripted rows; tests assert the **sequence of calls** and arguments (paging, `$filter` bounds) for a given plan.

**TDD loop:** implement the **orchestrator** (steps 1–8) against the interface; fakes prove you **request the right diffs** and **apply in the right order** (per table order if you add dependency rules later).

### 3. SQLite state (file or in-memory)

- Migrations or schema create; **repository** that loads/saves `safeThroughTimestamp` and `sync_runs`.  
- Tests use **temp file** or `sqlite` in-memory; assert **checkpoint only advances** after a successful verify (property: “no advance on failed verify”).

### 4. Contract / integration tests (real HTTP, not necessarily FileMaker)

- **OData** contract tests against a **recorded stub** (e.g. `httptest` in Go) or a **local mock server** that returns fixed OData JSON for specific URLs.  
- Optional: run against a **real dev FileMaker** server in CI only (gated) to catch filter/pagination quirks.

### 5. End-to-end (few, slow)

- One **happy path:** two fakes or one FM test double, full `run` once, checkpoint moves.  
- One **verify failure** path: second manifest pass returns a mismatch → checkpoint **unchanged**.

### What to avoid in TDD for this project

- **E2E-only** tests (flaky, slow, hard to get LWW edge cases).  
- **Testing full FileMaker** in every unit test.  
- **Asserting on log text** as the main contract (use structured results / return values).

### Suggested test layers (summary)

| Layer        | What you prove |
|-------------|----------------|
| Domain      | LWW, deletes, window math, verify match — **deterministic** |
| Orchestrator| Step order, no hydrate without diff, no checkpoint without verify |
| Adapters    | Correct OData URLs, auth header, pagination loop |
| E2E         | One container run, real volume + SQLite file (optional) |

### Incremental testing as you build

Ship **small vertical slices** and add tests **alongside each slice** (not only at the end):

| Milestone (example) | What to lock with tests |
|---------------------|-------------------------|
| Window + checkpoint only | Window bounds from SQLite state; no OData yet |
| Manifest client only | Paging, `$filter`, decode to `(id, timestamp)` — stub HTTP |
| Compare + plan only | Pure tests from fixture manifests |
| Apply + verify loop | Fake OData counts writes; verify phase replay |
| Full `run` | Two endpoints (fakes or real FM — below) |

Each milestone should leave **main green** with fast tests; slower tests tagged (`integration`, `fm`) for CI toggles.

### Two FileMaker files (why two)

Bidirectional sync **cannot be honestly exercised against one database**. You need **two hosted `.fmp12` solutions** (or two databases on distinct OData bases) so that:

- **Server A** and **Server B** have **independent state**, drift, and deletes.
- You can introduce change on **one side only** and prove the other receives it (and the reverse).
- Blue/green narratives match reality: two files, two URLs in config.

**Development convention:** maintain **dev File A** + **dev File B** (copies of production schema, anonymized data). Point Free Sync’s config at their OData URLs; wipe or reset those files when tests require a clean slate.

### Two fixture files (optional, no FileMaker)

For **fast regression** without spinning FM, keep **checked-in snapshots** (e.g. `testdata/manifest_blue.json`, `testdata/manifest_green.json`) representing manifest rows after a run. Tests feed them into the **compare / verify** logic as golden inputs. This complements—not replaces—integration tests against the two dev files.

---

*If you hand this to a Go dev, they can build it cleanly with the above test shape.*
