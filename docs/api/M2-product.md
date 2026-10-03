# M2 product API and operational boundary

M1.4–M1.6 contracts remain authoritative. M2 adds no schema migration.

## Product-quality additions

- Catalog list routes accept `order=title` (default) or `order=title_desc`.
  Cursors are scoped to order and existing filters; a cursor from another
  order is rejected. Album Track order remains disc/Track order.
- Artist and Album shapes add `artwork_url` and `track_count`; Albums add
  `artist_credit`. URLs remain public logical Track artwork URLs. Counts
  describe retained membership, including unavailable Tracks, rather than
  inventing a playable count. This avoids one metadata request per cover.
- `POST /api/v1/favorites/lookup` is a read with `{track_ids:[...]}` and returns
  the favorited subset in `{track_ids:[...]}`. Maximum 200 valid public Track
  IDs; no receipt or mutation is involved. Clients check visible membership
  rather than traversing the full saved set.
- `GET /api/v1/playlists?q=...` adds bounded literal name matching. `%` and `_`
  remain literals. Query is at most 120 code points/512 UTF-8 bytes. Cursors
  are scoped to the query. Playlist shapes add the exact `track_count`.
- `POST /api/v1/queue/select` takes `{item_id,expected_version}` and selects
  that existing available occurrence with a fresh generation. It does not
  insert a duplicate. Standard receipt/replay/conflict behavior applies.
- `POST /api/v1/queue/collection` takes `{track_ids,placement,expected_version}`,
  with 1–1,000 IDs and `now`, `next` or `end`. It preserves input order and
  duplicates and atomically commits insertion, order, selection and receipt.
  Invalid references, capacity and stale revisions commit no prefix.

Ordinary Play follows Up Next; the explicit "Play this song only" action
preserves solo playback. Client-local pending play intents are serialized and
the latest intent owns audio startup. Other-tab selection changes never lend
a newer token to an old decoder. A manually requested song may continue with
its own observed token and is described as separate from the changed queue.
Natural completion, atomic final reports, receipt retries and no-autoplay
reload behavior remain the M1 contracts. See ADR-010.

## Local catalog search

`GET /api/v1/search?q=...&limit=20` returns `{tracks: Track[], artists: Artist[], albums: Album[]}` using the existing public shapes. Query is trimmed; blank returns empty arrays. Maximum query is 120 Unicode code points and 512 UTF-8 bytes. Invalid UTF-8, NUL characters, duplicate query/limit fields and limits outside 1–50 return `400 invalid_request`. Limit applies independently to each group. No pagination or total count is implied; narrowing the query finds more specific results.

Matching is PostgreSQL case-insensitive literal substring matching on raw Track title/artist/album and group display text. `%` and `_` are literals. There is no typo tolerance, accent removal, semantic retrieval, external catalog or recommendation behavior. Results sort by existing normalized display keys and opaque IDs. Rootless legacy Tracks remain excluded; unavailable catalog entries remain visible. This is not a snapshot across all three sequential queries.

Request cancellation and a three-second deadline cover readiness and all queries. Failures use existing typed catalog errors; responses are `no-store`. Logs record request ID, duration, failure and cancellation, never search text or paths.

## Host administration

When a database is configured, the same Go executable opens a separate loopback listener at `127.0.0.1:8081` by default. Use `-admin-addr 127.0.0.1:<port>` or `-admin-addr ""` to disable it. IPv6 loopback is also supported. Non-literal or non-loopback bind addresses are rejected. A port conflict fails startup explicitly.

Open that address **on the server computer**. The music listener contains no host routes or host files. Do not reverse-proxy, tunnel or forward the admin listener. Peer IP and listening socket must both be loopback; Host must be a loopback name/address with the local port. Origin, when present, must exactly match. Cross-site and same-site-but-cross-origin Fetch Metadata is rejected. Forwarded headers are ignored. Writes require `Content-Type: application/json` and `X-Resonance-Admin: 1`; no CORS preflight is allowed. This protects against LAN clients, DNS rebinding and browser cross-origin writes. It does not authenticate malicious software already running on the host.

All responses are `no-store`, same-origin CORP, nosniff and frame-restricted. Paths occur only in this host response type; native root identity bytes and secrets are never returned.

| Host route | Behavior |
|---|---|
| `GET /api/v1/admin/status` | Process, catalog readiness, existing coordinator snapshot and configured listener address/mode; a wildcard bind has no invented device URL |
| `GET /api/v1/admin/roots?after=<UUID>` | At most 100 entries `{items,next}`; root fields plus host `path` |
| `POST /api/v1/admin/roots` | `{name,path}`; name ≤128 bytes, absolute local path ≤4096 bytes, body ≤8 KiB |
| `POST /api/v1/admin/roots/{id}/verify` | `{confirm:true}`; explicit identity pin/rebind |
| `POST /api/v1/admin/roots/{id}/enable` | `{}`; current identity must match pinned verified identity |
| `POST /api/v1/admin/roots/{id}/disable` | `{}`; retains files and catalog/user-library intent |
| `POST /api/v1/admin/roots/{id}/scan` | `{}`; foreground authoritative scan, up to five minutes, request cancellation |

Enrollment rejects relative paths, network/UNC paths, control characters and alternate stream syntax before canonicalization. Existing canonicalization, directory readability, overlap transaction and identity capture apply. Alias resolution is validated again; no directory browse or delete operation exists. Root actions use existing storage/scanner methods. A manual scan cannot override unverified/quarantined/disabled roots or the global scan lease.

Safe typed host errors include `host_only` (403), `invalid_request` (400), `not_found` (404), folder overlap/disabled/unverified/quarantined/changed or running scan (409), unavailable folder/identity (422), and `operation_unavailable` (503). Failed/uncertain enrollment is resolved by refreshing folders; a retry may report overlap. Verify/enable/disable are operator operations, not user-library receipt mutations. Scan results retain authoritative scan run IDs. Configuration/watch diagnostics may lag host changes by the existing 60-second refresh.

## PWA and browser behavior

The manifest names Resonance, uses original 192px/512px icons and standalone display. The worker script is versioned by a digest of embedded assets. Install precaches only the explicit static shell list. API, writes, artwork, Range/audio, demo and host requests never enter Cache Storage.

A controlled listener document and assets come from the same cached shell version. Updates wait until old clients close; no `skipWaiting`, client takeover or automatic reload can interrupt playback. The next session uses the newly activated version and deletes older Resonance shell caches.

On localhost, secure-context APIs may provide offline shell and installation. Trusted LAN HTTP generally cannot provide those capabilities; live catalog, queue and playback remain available. Future HTTPS can enable them where the browser supports them; HTTPS setup belongs to M3. Installation prompts and Media Session controls are browser-dependent. There are no music downloads, offline mutations or claims of offline music playback.

An offline shell says **Resonance server unavailable**. Restoring connectivity refreshes live state; reload never autoplays a saved queue selection. Decoder errors, unknown duration and missing/broken artwork have explicit fallbacks.

The refined player can prepare a saved selection's display metadata after reload without setting audio src or starting a listening session. An explicit Play checks the observed queue item/token and resumes that occurrence without another queue insertion. The full player's queue rail shows at most 20 entries and uses the existing revision/receipt mutations. Reading or refreshing it does not authorize stale playback to adopt a newer selection token. Generated artwork is a neutral missing-art treatment; album/Home cards use quiet artwork and actual title initials. Playlist signal marks are original collection decoration, not album artwork. Valid embedded artwork takes priority. Fonts, library icons and generated app assets are static shell resources, not catalog/media downloads. Mobile app options expose installation only when the browser provides an install prompt.

## Folder selection and artwork corrections (2026-10-03)

Library Track/Artist/Album lists, group Track lists and literal Search include
only identities with at least one location in an enabled enrolled folder. A
missing file within an enabled folder still appears as unavailable. Track detail
and durable collection references are retained when a folder is disabled, so
re-enabling or restoring music does not lose listener-owned state. Alternate
copies in enabled folders preserve visibility; group counts count Track
identities once. Adding a folder is additive, not a destructive replacement.

The artwork response verifies stored bytes/hash, bounded extraction and image
configuration. For admitted PNG/JPEG covers, the decoded format determines the
response Content-Type even if the tag labelled PNG bytes JPEG or vice versa.
No image bytes are changed. Unsupported images, invalid data, changed hashes,
oversized images and unsafe locations remain rejected. Generated sleeves apply
only when embedded artwork is absent or cannot be served safely.

## Playback modes (user-authorized 2026-10-03)

Shuffle and repeat are local browser preferences; they survive reload but never
start audio on reload. Both players expose stateful keyboard buttons; phones
use the full player to retain a compact dock. Repeat cycles off → all → one → off.

Shuffle changes the server's actual upcoming occurrence order through the existing
revision/receipt-protected reorder API. Current and previous occurrences remain
fixed. Turning it off restores surviving known upcoming occurrences where
possible, keeping explicit new additions in their slots. Duplicate songs retain
distinct occurrence IDs. With shuffle enabled, Play album/playlist shuffles the
new collection before its atomic insertion. Play next preserves explicit intent.

`POST /api/v1/queue/advance` accepts optional `repeat` (`off`, `one`, `all`).
Missing means off and existing callers retain their behavior. Repeat one applies
to natural endings; manual Next still advances. Repeat all wraps Next/Previous
and natural ending, skips unavailable occurrences in one bounded traversal and
stops if nothing is playable. Final listening report, selected occurrence,
fresh selection token, revision and receipt commit together. Receipt replay and
stale-token rejection are unchanged. Solo-song repeat restarts only after saving
its terminal report and never advances a separate saved queue.

## Library playback context and transport boundaries (2026-10-03)

Selecting a row in Library/Artist/Album Track views starts the playable songs on
that currently displayed page, in its displayed order, at the selected song.
The bounded page (at most 50 catalog rows) becomes the actual durable queue;
Next/Previous therefore follow visible music instead of an isolated insertion.
Navigation afterwards does not change playback context. An explicit Track
menu Play now/Play next/Add to queue retains its individual-occurrence semantics.

`POST /api/v1/queue/collection` additionally accepts `placement: "replace"`
and optional zero-based `start_index` (default 0). Only replace admits a nonzero
start index. Validate references, bounds and the selected Track's availability
before replacing any queue items. Replacement, order, fresh selection, revision
and receipt commit in one transaction. A failed/stale/unavailable replacement
preserves the prior queue. Track identities, favorites, playlists and history
are never deleted. A repeated receipt never replaces the queue twice.

Shuffle starts with the explicitly selected song, shuffles the remaining
context and retains restoration ranks for the new occurrence IDs. Disabling
shuffle preserves current playback authority while restoring surviving upcoming
songs where possible. Transport is single-flight across full/compact/queue
controls. Next is disabled when no playable next song exists unless Repeat all
can wrap; Previous restarts a currently playing song after three seconds, or
selects the prior available occurrence. A boundary input never pauses the song
or sends an empty advance. Detached decoders remain unable to control a newer
queue. A rejected old natural-ended request refreshes presentation without
adopting another selection token.
