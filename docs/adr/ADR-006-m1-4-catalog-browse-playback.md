# ADR-006 — M1.4 conservative catalog browsing and real-track playback

## Context

M1.3A makes Track, MediaObject, and MediaLocation authoritative. Raw Track tags and their metadata-source location are retained, while M0 serves a diagnostic WAV with correct HTTP byte-range behavior. M1.4 needs browse identities and a path from a public Track ID to enrolled media without confusing tags with real-world artist or release identity.

## Decision

`catalog_artists` and `catalog_albums` are local groupings with opaque IDs, normalized comparison keys, evidence scope, rule version, and provenance. Track memberships retain their source location, metadata snapshot, role/evidence code, and rule version. The original Track tags are never overwritten by grouping. No Artist or Album is fabricated for missing tags. A full composite credit stays a single credit; normalized name alone never merges artists, and title alone never merges albums. Exact-byte copies keep one Track and its established metadata-source precedence. Unavailable Tracks retain their last memberships.

Comparison uses versioned Unicode NFC, collapsed whitespace, and Unicode case folding without removing accents or punctuation. Album grouping requires a title and root-local folder evidence, compatible album-artist credit (or track artist when absent), and compatible years. A recognized disc folder may use its parent as evidence when tags agree. Missing years join only an unambiguous compatible group. Explicit common album-artist credit allows compilation tracks with different track credits. Artist reuse across albums requires the shared parent folder's normalized name to match the full credit; a generic shared parent is not artist evidence. Otherwise the credit is album- or Track-scoped. Conflicts remain separate or ungrouped. Unchanged rescans and unambiguous moves keep IDs; genuine splits/combinations retain old rows as provenance and assign new memberships transactionally. This corrected rule is version 2; an already completed version-1 grouping must be rebuilt before readiness succeeds.

Migration 0006 adds the grouping tables and browse indexes. Schema application and grouping backfill are distinct steps. A singleton grouping-state row records the rule version and completed backfill. `-migrate-only` retries idempotent transactional backfill; readiness rejects an incomplete state. Publication refreshes affected memberships inside the M1.3A final transaction. Added transaction time is measured separately.

All public routes live under `/api/v1`: `tracks`, `artists`, `albums`, individual details, artist albums/tracks, album tracks, Track stream, and Track artwork. Lists use opaque keyset cursors, default limit 50, maximum 200, stable ID tie-breakers, and bounded group/availability filters. Subsequent pages can observe a later reconciliation. Results contain display tags, grouping IDs, known availability, format, and logical URLs, never root IDs or filesystem paths. Rootless legacy records do not enter public browsing.

The stream resolver considers available locations under enabled enrolled roots, preferring the metadata-source copy then deterministic alternatives. It verifies that the enrolled root still resolves to its stored canonical path, validates stored relative paths, rejects link/reparse and Windows stream components, opens beneath `os.Root`, checks regular-file and observed stat/native evidence, and rechecks that the opened handle still occupies the confined path before headers. It tries another candidate when a check fails. An already-open handle remains the source after reconciliation. Demo and real routes share the M0 byte-serving function. A post-header read failure aborts the transfer without appending an error body; a pre-header failure keeps its safe HTTP error response. Embedded artwork is served only after bounded extraction, PNG/JPEG verification, and stored-hash verification.

Typed public failures are `invalid_request` (400), `not_found` (404), `track_unavailable` (503), `catalog_unavailable` (503), and `catalog_invalid` (500). Range failures retain M0's 400/416 behavior and headers. Logs contain codes and logical IDs only.

## Alternatives considered

- Global uniqueness by artist name or album title: rejects distinct real-world identities and editions without enough evidence.
- Tags as Track identity: violates M1.3A byte and continuity semantics.
- A second media handler: risks Range and cancellation divergence from M0.
- Hashing complete media on every request or extracting artwork without bounds: unnecessary work and unbounded exposure.

## Consequences

Browse may show same-name Artist or Album entries. Missing or ambiguous tags are displayed as ungrouped Tracks. PostgreSQL readiness depends on grouping completion. M0 remains an independent diagnostic route. This milestone does not add search, user-library state, transcoding, authentication, remote access, watchers, or distributed infrastructure.

## Evidence

Acceptance requires populated 0005 upgrades and retry tests; grouping edge cases; publication atomicity and cost; route and path-boundary contracts; MP3/FLAC/WAV Range parity, failure and cancellation tests; bounded artwork rejection; browser and same-LAN playback; PostgreSQL interruption; and raw latency measurements with actual overlap timestamps. Evidence is recorded separately as it is produced, not inferred from this decision.

Implementation evidence — 2026-09-23: populated upgrade/retry and exact schema drift tests, atomic publication fault injection, grouping edge cases, real-format HTTP integration, artwork rejection, PostgreSQL restart, three M1.4 and three M0 Chrome tests, and all Go unit/integration regressions passed. Independent review added generic-parent Artist separation, version-1 grouping repair, redirected-root and post-open path-swap rejection, and pre-header error-response regressions. [M1.4 measured evidence](../benchmarks/M1-4.md) links raw query, Range, publication, overlap, and fault artifacts. The physical same-LAN phone demonstration remains outstanding.

## What would cause reversal

An observed incorrect grouping with stronger local evidence, a measured publication cost that compromises reconciliation, or a protocol failure in shared byte serving would require a documented revision and new tests.
