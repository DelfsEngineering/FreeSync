# OData connectivity check — deng3.bfoperations.com (dev)

Last verification: after extended privileges / account updates. Auth is the service account in `config/dev.local.json` (not committed).

## Service root

`GET https://deng3.bfoperations.com/fmi/odata/v4` → **200** — JSON `value` lists hosted databases and container URLs.

## URL segment vs `.fmp12` name

OData path segments are the **catalog name** on the server (case-sensitive), not always the same as the file on disk.

| Role | Use this URL segment on deng3 | Notes |
|------|--------------------------------|--------|
| blue | **`BF_DevTeam_Test`** | Capital **T** in `Test`, not `BF_DevTeam_test`. |
| green | **`BF_Examples`** | Catalog shows `BF_Examples` (not `BBF_Examples`). |

## Current test results (both target files)

| Database | `GET .../$metadata` | Approx. size | `GET ...` (JSON service document) |
|----------|------------------------|----------------|-------------------------------------|
| `BF_DevTeam_Test` | **200** | ~196 KB XML | **200** |
| `BF_Examples` | **200** | ~203 KB XML | (not re-printed; same pattern as blue) |

Entity sets (rough count from `<EntitySet` tags in `$metadata`): **~39** on `BF_DevTeam_Test`, **~42** on `BF_Examples`.

## If access fails again

- **(9) Access denied** — privilege set / extended privilege for **OData API** on that file for this user.
- **(802) Unable to open file** — wrong segment name or file not hosted; always confirm the exact `name` / `url` from `GET /fmi/odata/v4`.

## Quick retest

```bash
curl -sS -u 'Charles Delfs:PASSWORD' \
  -o /dev/null -w '%{http_code}\n' \
  'https://deng3.bfoperations.com/fmi/odata/v4/BF_DevTeam_Test/$metadata'

curl -sS -u 'Charles Delfs:PASSWORD' \
  -o /dev/null -w '%{http_code}\n' \
  'https://deng3.bfoperations.com/fmi/odata/v4/BF_Examples/$metadata'
```

Expect `200` for both.
