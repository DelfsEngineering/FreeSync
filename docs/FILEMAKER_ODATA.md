# FileMaker Server — OData (for Free Sync)

Primary reference: **Claris FileMaker OData API Guide**  
[https://help.claris.com/en/odata-guide/content/odata-guide.html](https://help.claris.com/en/odata-guide/content/odata-guide.html)

## URL shape

| Piece | Meaning |
|--------|---------|
| `https://{host}/fmi/odata/v4` | List databases / service root ([Get database names](https://help.claris.com/en/odata-guide/content/get-database-names.html)) |
| `https://{host}/fmi/odata/v4/{database-name}` | Database segment — **database-name** is the hosted file name **without** `.fmp12` ([Write OData API calls](https://help.claris.com/en/odata-guide/content/write-odata-api-calls.html)) |
| `…/$metadata` | XML schema for entity sets, fields, keys |

Example segment only:

```text
/fmi/odata/v4/ContentMgmt
```

## Authentication

- **HTTP Basic** with a **FileMaker file account** (user must exist in that database with a password).  
- See: [Creating an authenticated connection to the host](https://help.claris.com/en/odata-guide/content/creating-authenticated-connection.html)

Header: `Authorization: Basic base64(user:password)`  
Account names can include spaces; encode correctly in Basic auth.

## Methods (typical sync usage)

From [Write OData API calls](https://help.claris.com/en/odata-guide/content/write-odata-api-calls.html):

- **GET** — metadata, collections, records (with OData query options)
- **POST** — create record (and other admin operations)
- **PATCH** — update record
- **DELETE** — delete record

## Headers

- **`Authorization`** — required on all calls  
- **`Accept`** — often `application/json`; optional `IEEE754Compatible=true` for JSON number handling per docs  
- **`OData-Version` / `OData-MaxVersion`** — FileMaker Server supports **OData 4.0** per guide  
- **`Prefer`** — OData options plus FileMaker-specific values (e.g. `fmodata.basic-timestamp`, `fmodata.include-specialcolumns` for ROWID/ROWMODID)

## Querying data

Use OData standard query options on entity sets (see guide sections **Query options** and **Request data**). Free Sync will lean on:

- **`$select`** — manifest fields (`id`, modification field)
- **`$filter`** — time window on modification field
- **`$orderby`** — stable pagination
- **Paging** — next links / page size per server behavior

## Metadata

- [Get metadata](https://help.claris.com/en/odata-guide/content/get-metadata.html) — discover entity set names and property types for each table.

## Security note for repositories

Do **not** commit host-specific URLs with real passwords. Use `config/dev.example.json` in git and keep secrets in **gitignored** `config/dev.local.json`.
