# M1.5 user-library API

M1.5 adds one durable queue, playlists, Track favorites, and recent qualifying listening history under `/api/v1`. All Track references must be public rooted catalog IDs. Unavailable Tracks stay in user-library state. The existing M1.4 Track stream and Range routes are unchanged.

## Queue

`GET /queue` returns `{revision,current_item_id,selection_token,selection_state,items}`. Each item has an opaque queue-item ID, Track ID, dense position, display title/credit, catalog-known `available`, and optional coded last skip. `selection_state` is `stopped` or `selected`; it does **not** assert that any browser is playing. The opaque `selection_token` is a global generation marker, not authentication or a tab lease.

All writes include `expected_version`. Non-naturally-idempotent writes require a UUID `Idempotency-Key` header. Queue writes return a compact revision/selection result; clients then refetch `GET /queue`.

| Route | JSON request |
|---|---|
| `POST /queue/items` | `{track_id,placement,expected_version}`; placement is `end`, `next`, or `now` |
| `DELETE /queue/items/{item_id}` | `{expected_version}` |
| `PUT /queue/order` | `{item_ids:[...],expected_version}`; exact permutation of all item IDs |
| `DELETE /queue` | `{expected_version}` |
| `POST /queue/advance` | `{direction,expected_version,expected_current_item_id,selection_token}`; direction is `next`, `previous`, or `ended` |

`now` inserts a new occurrence after the former current item and selects it. `next` inserts immediately after current; the most recent Play Next is next. Duplicate Tracks have distinct item IDs. Reorder preserves the current item and selection token. Next/ended skip unavailable items, report `skipped_item_ids`, and retain those items. When no later item is playable, state becomes stopped with the last current item retained and its token invalidated. Previous chooses an earlier available item; the browser may seek current audio to zero instead when position exceeds three seconds. A stale item/token pair returns `409 stale_selection` before it can advance twice.

For a queued natural `<audio ended>`, `POST /queue/advance` must also contain `session_id` and `final_report` with `direction:"ended"`. The server validates and commits the final report, completion state, queue selection, and receipt in **one transaction**. A repeated request with the same key returns the stored result. A new key with an older token is rejected. A resolver failure may be sent as `failure_code:"resolver_failed"` with a manual next action.

## Playlists and favorites

`GET /playlists?limit=&cursor=` lists playlists. `POST /playlists` takes `{name,expected_version:0}`. `GET /playlists/{id}` returns playlist metadata, revision, and ordered entries with catalog-known availability. `PATCH /playlists/{id}` takes `{name,expected_version}`; `DELETE /playlists/{id}` takes `{expected_version}` and returns 204 on repeat. Playlist entry routes are `POST /playlists/{id}/items` with `{track_id,expected_version}`, `DELETE /playlists/{id}/items/{item_id}` with `{expected_version}`, and `PUT /playlists/{id}/order` with an exact `{item_ids,expected_version}` permutation. Mutations except naturally repeatable playlist deletion require an idempotency key. Playlist deletion cascades only to playlist entries.

`GET /favorites?limit=&cursor=` lists favorite Tracks. `PUT` and `DELETE /favorites/tracks/{track_id}` return 204 and are naturally idempotent. Favorites target Tracks only. List pages use validated opaque keyset cursors; default limit is 50, maximum 200. A later page may observe intervening writes.

## Listening sessions and history

The browser calls `POST /listening-sessions` with `{id,track_id,client_instance_id}` and optional `{queue_item_id,selection_token}` only after `audio.play()` succeeds and media time advances beyond 0.1 seconds. The IDs are client-generated UUIDs. A later start from the same client instance closes an unfinished prior session as interrupted. Preload, HTTP 200/206, Range requests, and decoder failures create no session.

`PUT /listening-sessions/{id}/report` takes monotonically increasing `{sequence,listened_ms,position_ms,duration_ms,seek_count}` and optional `terminal_reason` (`stopped`, `decoder_error`, `disconnected`, or `ended`). Values are cumulative; duplicate/older sequences return existing state. A queued `ended` report is accepted only inside `/queue/advance`. For a known duration, meaningful listening requires `min(30s,max(1s,duration/2))`; unknown duration uses 30s. A natural end with unknown duration may still finalize the session and advance its queue, but cannot mark the Track completed. With a known duration, completion additionally requires enough active listening and a final position within two to five seconds of the known end. After any seek, completion also requires at least one second of reported active listening and forward position progress after the latest stored seek report; a seek-to-end alone can therefore be meaningful but not completed. These are bounded unauthenticated client reports, not proof of audible playback or authoritative live position.

`GET /history?limit=&cursor=` lists sessions with `meaningful_at` or `completed_at`. Short starts, failed decodes, and Range-only traffic do not appear. The browser may keep one bounded pending cumulative report or ended transition in `sessionStorage` for retry after an outage. Refresh reloads durable queue selection but does not autoplay.

## Errors, limits, and operations

Public failures use `{"error":{"code":"...","message":"..."}}`: `invalid_request` (400), `not_found` (404), `stale_version`, `stale_selection`, `idempotency_conflict`, or `limit_exceeded` (409), and `catalog_unavailable` (503). A successful retry is looked up **before** version validation and returns `Idempotency-Replayed: true`. The same key with different canonical request bytes is `409 idempotency_conflict`. Receipts hold at most 256 KiB canonical request bytes and 16 KiB result bytes; they expire after seven days. Startup, hourly bounded cleanup, and write-time cleanup remove expired receipts. There is no generic job or event system.

The queue cap is 1,000 items, each playlist cap is 5,000 items, and JSON bodies are capped at 256 KiB. No API response or receipt contains host or root-relative paths. Set `RESONANCE_DATABASE_URL`, run `-migrate-only` to apply 0007, then start the server. Normal startup rejects a pending or incompatible schema. PostgreSQL loss returns typed 503 for new user-library operations; an already-open M1.4 audio stream may continue.
