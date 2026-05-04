# Free Sync V2: Webhook-Triggered Sync

This document describes a V2 feature set.  
V1 (current) remains checkpoint-window polling.

## Goal

Reduce time-to-sync by triggering sync work from FileMaker OData webhooks, while keeping periodic polling as a safety net.

## OData webhook endpoints

Per database:

- Create: `POST /fmi/odata/v4/{database}/Webhook.Add`
- List: `GET /fmi/odata/v4/{database}/Webhook.GetAll`
- Delete: `POST /fmi/odata/v4/{database}/Webhook.Delete({id})`

Current assumption: one webhook per table (`tableName` is singular).

## Registration model (idempotent)

On startup, do not blindly create hooks.

1. Build desired hook specs from config (one spec per table per side).
2. Call `Webhook.GetAll`.
3. For each desired spec:
   - If an equivalent hook already exists, keep it.
   - If missing, create it.
4. Optional cleanup: delete stale hooks that match Free Sync URL prefix but are no longer desired.

This prevents duplicate webhook growth during restarts or deploys.

## Circular event prevention

Webhook-based bi-directional sync must prevent ping-pong writes.

Required protections:

- **Origin marker**: stamp writes with source marker (or equivalent audit marker).
- **Idempotency cache**: drop duplicate/retried events by event key (`table:id:modTS` or provider id).
- **No-op guard**: skip write when destination payload already matches merged result.
- **Debounce per record**: collapse rapid events for same `table+id`.
- **LWW gate**: only apply when winner timestamp is strictly newer.

## Runtime shape

Hybrid flow:

1. Webhook receiver accepts event.
2. Receiver enqueues lightweight job (`server`, `table`, `id`, timestamp).
3. Worker resolves latest record(s), runs existing merge/apply logic.
4. Periodic polling still runs to backfill missed events.

## Operational notes

- Keep webhook receiver highly available; failed delivery may retry repeatedly.
- Track webhook delivery failures and processing lag in metrics/logs.
- Use bounded queues with dead-letter handling for poison jobs.
- Keep V2 behind a feature flag until stable in production.

## Suggested config additions (V2)

```json
{
  "webhooks": {
    "enabled": false,
    "ensureOnBoot": false,
    "receiverURL": "https://sync.example.com/webhooks/filemaker",
    "maxFailedAttempts": 10
  }
}
```

## Rollout plan

1. Build `ensure-webhooks` command (create/list/delete idempotently).
2. Implement receiver + queue + worker path behind feature flag.
3. Enable for a single low-risk table first.
4. Compare lag/error rate vs current polling-only mode.
5. Expand table coverage gradually.
