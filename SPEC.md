# fm-odata-sync — Specification

## System Name

**fm-odata-sync**

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
docker run fm-odata-sync run
```

runs once, exits

### Future

**serve** mode (HTTP API)

- start sync
- cancel
- status

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

---

*If you hand this to a Go dev, they can build it cleanly.*
