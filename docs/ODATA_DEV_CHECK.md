# OData connectivity check — deng3.bfoperations.com (dev)

Checked with Basic auth to the service account in `config/dev.local.json` (not committed; dev password is temporary).

## Service root

`GET https://deng3.bfoperations.com/fmi/odata/v4` → **200** — returns JSON `value` listing all hosted databases exposed to OData for this user’s **catalog** access.

## URL segment vs filename

OData uses the **database name** as in the **host’s catalog**, not always the same spelling as the `.fmp12` on disk. Segments are **case-sensitive** in practice.

| You said | OData catalog name on deng3 | Notes |
|----------|------------------------------|--------|
| `BF_DevTeam_test.fmp12` | **`BF_DevTeam_Test`** | Use this exact path segment in URLs (capital **T** in `Test`). `BF_DevTeam_test` is wrong. |
| `BBF_Examples.fmp12` | **Not in the catalog** | No `BBF_Examples` / `BBF_Examples` entry in the current `value` list. |

## `BF_DevTeam_Test`

| Request | Result |
|--------|--------|
| `GET .../BF_DevTeam_Test` (JSON) | **501** + `(9): Access denied` |
| `GET .../BF_DevTeam_Test/$metadata` | **Access denied** (error 9) |

**Interpretation:** The account can **see** the database in the top-level list, but does **not** have permission to open that file for **OData data/metadata** (FileMaker **Extended Privileges** / privilege set: turn on access for the **OData API** for this file, for the same user, and re-upload or grant in FileMaker if required — see Claris admin docs for the exact extended privilege name for your FileMaker version).

## `BBF_Examples`

| Request | Result |
|--------|--------|
| `GET .../BBF_Examples/$metadata` | **501** + `(802): Unable to open file` |

**Interpretation:** The server does not expose that database at this path. Typical causes: file **not hosted** on this FileMaker Server, different **hosted name**, or spelling (`BBF` vs `Bf`, spaces, etc.). Fix by confirming in **Admin Console** that `BBF_Examples.fmp12` is open and appears under OData; then use the **exact** name from `GET /fmi/odata/v4` for the green URL.

## Sanity reference (another DB)

`GET .../BetterForms_Test/$metadata` → **200**, large XML document — confirms Basic auth and OData **do** work on this host when the file allows this account.

## Actions before Free Sync can sync these two files

1. **BF_DevTeam_Test** — Grant the dev account **OData API** extended privilege on this file (same issue blocks `$metadata` and any sync).
2. **BBF_Examples** — Host the file on deng3 (or align name), confirm it appears in `GET /fmi/odata/v4`, then set green URL to that exact segment.
3. Keep **`config/dev.local.json`** updated with corrected URLs; never commit it.

## Optional quick retests (Terminal)

```bash
curl -sS -u 'Charles Delfs:PASSWORD' \
  'https://deng3.bfoperations.com/fmi/odata/v4' | python3 -m json.tool | grep -i examples

curl -sS -u 'Charles Delfs:PASSWORD' \
  'https://deng3.bfoperations.com/fmi/odata/v4/BF_DevTeam_Test/$metadata' | head
```

Expect `$metadata` to return XML starting with `<?xml`, not `m:error`.
