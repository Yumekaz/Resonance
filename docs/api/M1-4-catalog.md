# M1.4 local catalog API

All routes are under `/api/v1`. The server must be configured with a migrated, grouped PostgreSQL catalog. The diagnostic `/api/v1/demo-track` and `/media/demo-track` remain separate. Only enrolled-root Track history is public; rootless legacy records are not browsable.

## Read routes

| Route | Shape |
|---|---|
| `GET /tracks` | `{ "items": [Track], "next_cursor": string|null }` |
| `GET /tracks/{id}` | `Track` |
| `GET /artists` | `{ "items": [Artist], "next_cursor": string|null }` |
| `GET /artists/{id}` | `Artist` |
| `GET /albums` | `{ "items": [Album], "next_cursor": string|null }` |
| `GET /albums/{id}` | `Album` |
| `GET /artists/{id}/albums` | Album page for recorded artist roles |
| `GET /artists/{id}/tracks` | Track page for recorded artist roles |
| `GET /albums/{id}/tracks` | Track page in disc number, track number, normalized title, ID order |
| `GET`, `HEAD /tracks/{id}/stream` | Direct bytes through the shared M0 Range implementation |
| `GET /tracks/{id}/artwork` | Verified embedded PNG/JPEG, when present |

List requests accept `limit=1..200` (default 50) and an opaque `cursor`. `/tracks` also accepts one `artist_id`, one `album_id`, and `available=true|false`; `/albums` accepts `artist_id`. Unknown query parameters do not change the query. Track, Artist, and Album lists sort by versioned normalized display text and opaque ID. Pages are point-in-time reads; a concurrent reconciliation may change a later page. Cursors bind the route and filters and are validated before database access.

`Track` contains `id`, raw `title`, `artist_credit`, `album_title`, `album_artist_credit`, `track_number`, `disc_number`, `release_year`, `genre`, optional `artist_id`/`album_id`, a format summary, `available`, `stream_url`, and optional `artwork_url`. Missing tags remain `null`; the UI supplies presentation fallbacks. `Artist` contains `id`, `display_credit`, and `available`. `Album` contains `id`, `display_title`, optional `album_artist_id`, optional `release_year`, and `available`. Two Artist entries may have the same display credit because grouping IDs represent local evidence, not a verified real-world person.

The API never returns canonical roots, relative paths, root IDs, or raw database errors. Encoded media is not transcoded. Browser codec support is independent of successful HTTP delivery.

## Failures and streaming

JSON failures have `{"error":{"code":"...","message":"..."}}`. `invalid_request` is 400 for malformed IDs, cursors, filters, and limits; `not_found` is 404 for unknown catalog IDs or unavailable artwork; `track_unavailable` is 503 when no candidate copy can be opened; `catalog_unavailable` is 503 during PostgreSQL failure or incomplete grouping; `catalog_invalid` is 500 for unusable stored display metadata. `/health` remains process liveness. `/ready` includes PostgreSQL schema and completed grouping state.

Stream requests preserve M0 full GET, single Range, HEAD, ignored `If-Range`, 400 malformed Range, and 416 unsatisfiable Range behavior. A range response has `Accept-Ranges`, `Content-Range`, and correct `Content-Length`. Each candidate's enrolled root is checked against its stored canonical path, then the file is opened beneath that root and checked against stored size, mtime, applicable native handle evidence, and a final confined path stat. A legacy location without both scan observations must be rescanned before it can stream. The server tries another available copy before headers if one fails. Once streaming starts, it reads the already-open handle even if a scan changes catalog state. Truncation or read failure after headers aborts the connection; a pre-header failure returns its safe HTTP response, and no JSON is appended to media bytes after headers. Request telemetry distinguishes `source=demo` and `source=catalog` and contains logical IDs and coded failures only.

The artwork route opens an enrolled source, reuses the M1.1 bounded metadata reader (8 MiB cumulative read, 2 MiB artwork ceiling), checks PNG/JPEG decoding and dimensions, and compares extracted bytes with the stored SHA-256. A missing, changed, oversized, unsupported, or mismatched picture is not served.

## Operator sequence

Set `RESONANCE_DATABASE_URL` as described in [the PostgreSQL runbook](../runbooks/M1-1-postgres.md). Run `resonance -migrate-only` to apply migration 0006 and its transactional grouping backfill; retry that command after interruption. Enroll and scan with `resonance library add` and `resonance library scan` as in [the library runbook](../runbooks/M1-2-library.md). Start the server and open `/` for the library or `/demo` for M0 diagnostics. The server refuses catalog readiness while a 0006 backfill is incomplete.
