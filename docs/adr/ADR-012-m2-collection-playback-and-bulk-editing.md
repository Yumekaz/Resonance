# ADR-012: M2 whole-collection playback and collection editing

## Context

The owner prioritizes whole-library playback, bulk queue/playlist organization,
then in-playlist search/sort and density. The current 50-row playback context
cannot reach later catalog pages. Queue capacity, receipt replay, selection
tokens and one persistent audio element must remain intact.

## Decision

Snapshot a chosen collection's ordered Track references in PostgreSQL when Play
starts. Keep a bounded rolling queue window, with a small previous-song buffer.
Refill and cycle within the existing locked queue-advance transaction, including
the natural-ended final report and receipt. The browser never downloads the
whole source merely to play it. New imports/source edits are reflected by starting
the collection again; they do not rewrite the current listening selection.

Source members retain occurrence positions, so playlist duplicates remain
distinct. Full-source shuffle orders unplayed members; toggling it preserves
the live selection and explicit queued additions. Resolver failures are excluded
for this listening context rather than retried indefinitely. Clear/replace stops
continuation. Revisions and tokens remain authoritative; a refresh cannot grant
another decoder's selection authority.

Add receipt-protected atomic batch operations for selected occurrence IDs. Reject
the whole operation on missing IDs, stale revisions, capacity or malformed input.
Copy/move across collections uses a consistent locking order. View search/sort
does not silently rewrite saved playlist order. Density is a local presentation
preference. Snapshot playback captures the chosen view order and query.

Migration 0009 adds only rebuildable listening-source state and its bounded queue
mapping, preserving populated catalog and user-library tables. Test populated
upgrade/restore, schema compatibility, rollback/fault injection and large-source
behavior before migrating the owner's database. No framework, service, cache,
broker, recommendations, transcoding or remote-access infrastructure is added.

## Alternatives considered

- Enqueue 10,000 rows: violates the existing queue cap and browser budget.
- Browser-only cursor: cannot establish durable continuation across reloads and
  cannot atomically couple refill with final-report/advance/receipt.
- Sequential bulk writes: partial application and ambiguous retries.
- Automatically sort saved order on a dropdown change: makes a viewing choice
  unexpectedly mutate a listener-owned playlist.

## Consequences

The source is a snapshot of playable references, not offline audio or a live
recommendation stream. Catalog changes are checked for availability as playback
advances. Previous-song browsing is bounded; it is not unbounded audible history.
Queue additions remain listener-controlled and play ahead of later source refill.
Removing an upcoming generated entry changes this round without editing its
source playlist. The existing queue and playlist capacities remain enforced.

## Evidence

The [collection verification](../benchmarks/M2-collections-verification.json)
records real PostgreSQL and Chrome checks, complete 350-reference traversal,
10,000-reference bounded startup/shuffle, exact playlist view/source order,
2,000-occurrence copying, failure rollback/replay and populated v8→v9 upgrade.
An actual owner backup was restored and upgraded in an isolated database.
Main-library promotion requires separate approval after an automatic approval
review rejection and has not run. Only the existing grouping completion timestamp is
excluded from before/after upgrade comparison; source and user-owned fields and
the previous migration ledger are compared exactly. Raw failed attempts remain
private and unchanged. [Real app captures](../design/m2/collection-quality/README.md)
record the responsive review. This evidence does not claim the complete M2
quality/device acceptance target has been met.

## What would cause reversal

Stale-decoder authority reuse, non-atomic mutation/replay, unsafe migration,
unbounded queue/DOM growth or measured unacceptable large-source latency requires
correcting or disabling the new source behavior while preserving existing data.
