# ADR-007 — M1.5 durable personal library state

## Context

M1.4 provides stable public Track IDs and a root-confined Range stream. A single-owner user needs a queue, playlists, Track favorites, and meaningful listening history that survive restart. Browser media playback remains transient; HTTP bytes alone do not prove listening.

## Decision

PostgreSQL stores one ordered active queue, a current item, revision, opaque global selection generation token, and `stopped`/`selected` state. It does not store actual browser play, pause, buffering, or live position. Duplicate Track occurrences have distinct queue-item IDs. Queue order is dense and capped at 1,000. Play Next inserts immediately after the current item; Play Now inserts after the prior current and selects the new item, retaining Previous. Next/ended skip catalog-known unavailable items with a recorded code; Previous selects the prior playable item. A stale current-item/token pair cannot change selection twice. The token coordinates all tabs; it is neither authentication nor a browser lease.

Playlists have stable IDs, versions, dense item positions, duplicate Track references, and a 5,000-item cap. Playlist deletion cascades only to its entries. Favorites are a naturally idempotent set of Track IDs. Every reference validates against the public rooted Track catalog, including retained unavailable Tracks.

Queue and playlist writes require `expected_version`; stale writes return `409 stale_version`. Non-naturally-idempotent writes also require a UUID `Idempotency-Key`. A bounded canonical request and compact result are stored with the mutation. Receipt lookup precedes version validation, so a successful retry replays its result; the same key with different canonical content returns `409 idempotency_conflict`. Receipts expire after seven days and carry creation, expiry, and cleanup timestamps; bounded cleanup removes expired rows during product writes in small batches. This is retry state, not generic event infrastructure.

The browser starts a session only after `audio.play()` succeeds and media time exceeds 0.1 seconds. It reports monotonic sequence, cumulative active listening milliseconds, position, optional finite duration, and cumulative seeks. Active time accrues only while playing, ready, and not seeking or stalled. The server rejects implausible or invalid reports and ignores duplicate/older sequences. Meaningful listening requires at least `min(30s, max(1s, duration/2))` for known finite duration, otherwise 30s. A natural end without known duration may still finalize the session and advance a queued selection, but cannot mark completion. Completion requires natural `ended`, qualifying listening time, and position near the known end. After any seek, completion also requires at least one second of listening and forward position progress after the latest stored seek report; seeking to the end alone cannot complete a Track. Short or failed sessions remain operational rows but are excluded from recent history. This is unauthenticated client evidence, not proof of audible sound.

Natural `ended` on a queued selection submits its final cumulative report to `/api/v1/queue/advance` and finalizes the session **in the same PostgreSQL transaction** that advances the queue and writes the receipt. A replay returns that result without a second completion or advance. Outside the queue, the report route may finalize independently. A server outage leaves one bounded pending ended transition for browser retry and pauses automatic progression.

## Public contract

Versioned `/api/v1` routes expose queue read/add/remove/reorder/clear/advance; playlist list/create/detail/rename/delete and entry add/remove/reorder; Track favorite list/put/delete; listening-session start/report and qualifying history. IDs are opaque. JSON and cursors are bounded. Typed failures are `invalid_request` (400), `not_found` (404), `stale_version`, `stale_selection`, or `idempotency_conflict` (409), and `catalog_unavailable` (503). No response, receipt, or log contains host paths.

## Alternatives considered

- Tab-local queue: loses ordering and selection at restart.
- Persisting browser play/pause/position in PostgreSQL: claims authority the server does not have.
- Two commits for ended finalization and advance: permits duplicate history or skipped items after interruption.
- A generic event bus, broker, or ownership lease: no M1.5 requirement earns it.

## Consequences

The database serializes each queue or playlist mutation through a row lock and revision. Multiple tabs may see stale versions and must refresh. Dense reorder is acceptable only while measured at bounded M1 sizes. The selection token is visible to local browsers and grants no access control. No authentication, remote access, search, watcher, transcoder, recommendation, or distributed component is added.

## Evidence

Acceptance requires populated migration and schema drift tests, receipt replay/conflict/expiry behavior, atomic ended transition and stale-token fault tests, Track continuity and unavailable/reappearing cases, history thresholds and failure cases, browser refresh/decoder/disconnect behavior, all earlier regressions, and raw benchmark/fault evidence. Evidence is recorded after execution rather than inferred from this ADR.

Implementation evidence — 2026-09-24: real PostgreSQL tests passed populated 0006→0007 upgrade, rollback/retry and exact contract drift; queue/playlist ordering and caps; receipt replay-before-version, conflict, expiry and cleanup; atomic ended rollback/retry; stale selection; favorites; cumulative history thresholds; restart persistence and unavailable/reappearing references. Final format, vet, unit, and serial full integration suites passed. Chrome passed 4/4 M1.5, 3/3 M1.4, and 3/3 M0 tests on the built server. [M1.5 measured evidence](../benchmarks/M1-5.md) links raw transaction timings, exact catalog/Range write-overlap timestamps, and PostgreSQL outage/recovery. No physical M1.5 phone-controls run was performed.

Independent review note — 2026-09-24: the review rejected the original submission for stale-tab selection use in the browser, unsafe rebinding of Play Now to a later selection, non-reusable browser idempotency keys after a lost response, ended-transition retries stuck on reorder-only version conflicts, incomplete queue-singleton readiness validation, seek-to-end completion after meaningful listening, and rejected unknown-duration natural ends. Corrections and focused regression tests are present in the working tree. The Go/PostgreSQL/Chrome checks above were not independently rerun after those changes because this review environment has no Go executable, PostgreSQL test server/client, configured test database, or running Resonance server. The evidence paragraph above describes the original submission and is not a pass claim for the corrected tree.

## What would cause reversal

Measured reorder cost, a concrete multi-tab failure, or a later authenticated multi-user requirement would justify revisiting the state model with a new ADR.
