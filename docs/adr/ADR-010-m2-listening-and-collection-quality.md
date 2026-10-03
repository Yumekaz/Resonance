# ADR-010 — M2 listening clarity and atomic collection actions

## Context

The strict M2 product audit found that a local song could disagree with the
durable queue, playing a queue occurrence inserted a duplicate, and album
queueing could commit only a prefix after a failure. These are earned product
requirements inside M2. No new infrastructure or migration is needed.

## Decision

Ordinary Play and Play now select a new queue occurrence, so Play next and
automatic advancement follow the music currently playing. Preserve single-song
playback as an explicit "Play this song only" action and preserve the persistent
audio element. Show that listening source separately from the saved queue,
including an explicit resume action. An observed item/token pair remains the
decoder's authority. Queue changes elsewhere cannot silently supply a newer
token. Rapid manual play intents are serialized and an older pending intent
cannot start audio after the listener has chosen a newer song.

Add `POST /api/v1/queue/select` to select an existing available occurrence using
its ID and `expected_version`. Issue a fresh selection generation. Add
`POST /api/v1/queue/collection` for 1–1,000 Track IDs and `now`, `next` or `end`
placement. Preserve input order and duplicate occurrences. Validate the whole
collection and total queue capacity before committing its insertion, order,
selection and receipt in one transaction. Both endpoints reuse existing
receipt-before-CAS behavior and bounded JSON decoding.

Extract the frontend API/retry boundary intact. A pure listening projection
owns presentation, not mutation authority or audio. Contextual action menus
are separate from input dialogs. Updates and connectivity have independent
presentation state; updates never interrupt playback.

## Alternatives considered

- Keeping single-song playback as the implicit default: leaves Play next
  targeting a different saved queue rather than the audible song. Single-song
  playback remains available through an explicit action.
- Sequential collection writes plus a progress counter: still allows partial
  application and awkward retries.
- Reusing Add Track to select an existing occurrence: creates a duplicate.
- A framework or service migration: does not earn the complexity here.

## Consequences

The new additive API routes retain existing queue/session/idempotency semantics.
Collection writes are bounded at the existing queue capacity. One batch uses
one revision and one compact receipt, rather than one per Track. Collection
completion can fail visibly as a whole; the interface does not claim success
for a partial addition.

## Evidence

`TestM2AtomicCollectionAndExistingOccurrenceSelection` uses PostgreSQL and covers
order, duplicates, rollback on bad references, capacity, stale versions,
receipt replay/conflict, fresh generation and rejection of an old decoder.
The quality escalation preserves every failed attempt and completed run. The
2026-10-03 uncached Go campaign passed 122 named unit checks and 262 named
integration checks, with the recorded privilege/opt-in skips. A full real
Chrome campaign passed 54/54 before the final naming/focus follow-up.

Dense queue ordering uses one parameterized UPDATE FROM unnest WITH ORDINALITY
inside the existing locked transaction, rather than 1,000 individual updates.
The same duplicate-occurrence, deferred ordering constraint and receipt tests
remain. On the measured warm 999-entry local fixture, ordering p95 decreased
from 302.853 to 39.160 ms; this is transaction evidence, not a phone/LAN SLO.

Catalog group artwork/credits/counts are bounded read projections rather than
one metadata request per cover. Visible favorite membership uses a coalesced
200-ID-bounded lookup with per-ID read/mutation fencing. Playlist destinations
are queried only when their paged searchable picker opens. No full saved-set
traversal is required at listener startup.

The API/retry kernel, listening session/report clock, pure listening projection,
favorite cache, contextual menu, occurrence motion, playlist song picker and
playlist destination picker each have explicit ownership. Navigation and saved
collection controllers have private scopes. The single audio element remains
the playback authority; projection subscriptions cannot select or advance it.
The final navigation pass adds bounded read-position descriptors and scoped
artwork projection coalescing. These never cache mutation authority or offline
catalogue responses. Queue rendering prioritises the current selection and
following occurrences, while explicit earlier-page browsing remains available.
The isolated artwork animation replaces full-page snapshots after profiling;
current rAF timing is not described as compositor FPS or physical-device proof.

## What would cause reversal

Measured transaction contention at the existing 1,000-item bound, a changed
product requirement for collection sequencing, or an independently reproduced
regression in the queue generation or retry contracts.

## Playback-mode extension (2026-10-03)

The owner explicitly requested shuffle and repeat. Keep mode preferences in the
browser; preserve the durable queue as the observable playback order. Shuffle
uses existing reorder/collection mutations rather than a second hidden queue.
An additive optional repeat intent on queue advance preserves the atomic final
report and fresh selection generation. No migration or new dependency is needed.
Off is the backward-compatible default. Natural Repeat one never traps manual
Next; Repeat all wraps once and stops when every occurrence is unavailable.
Regression tests cover real endings, occurrence identity, receipts, stale tokens,
queue changes, reload without autoplay and small-screen keyboard controls.

## Library context correction (2026-10-03)

Owner playback exposed an untested user journey: a Library row inserted only
one song, so Next immediately exhausted the queue and falsely appeared as a
queue change. The owner explicitly selected continuation through Library songs.
Use the current bounded catalog page as a playback context, replacing queue
occurrences and selecting its chosen index atomically. This earns additive
replace/start_index intent on the existing collection API, not a hidden local
queue or a migration. Explicit queue-add menu actions keep their contracts.

Transport controls derive actual playable previous/next availability, honor
Repeat all and run one step at a time. No-target input is a no-op; stale decoder
selection authority is never refreshed into live playback. PostgreSQL tests
cover replacement rollback, unavailable selection, duplicate occurrences,
receipts/conflicts and old-token rejection. Real browser regressions reproduce
the former failures and verify Library selection → Next → Previous with audio
still playing. Current-page scope keeps 10k browsing bounded; it does not silently
fetch or enqueue the entire catalog.
