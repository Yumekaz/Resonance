# M1.7 single-node closure evidence

Later verification, 2026-10-05: the two privilege-blocked file-symlink checks
identified below both executed with Administrator permission and passed without
skips. See [named results and raw hashes](benchmarks/M2-collections-verification.json).
This resolves that specific remaining G6/G8 requirement; the September campaign
and its original skips remain preserved below. It does not establish the complete
physical-phone G2 journey or change historical benchmark evidence.

Evidence dated **2026-09-28 UTC**, extending into September 29 Asia/Calcutta. Candidate base: `47c31c5e55ff89edffaa55a037762ce85496f63b` (`m1.6-watcher-recovery`) plus the corrected uncommitted tree. **M1 is not complete. No commit or push was made.**

The [public export index](benchmarks/m1-7-public/20260928T183200Z-closure/export-index.json) records original/export SHA-256 values and exclusions. Private raw attempts remain ignored and preserved without rewriting. The prior incomplete report is retained in `m17-prior-evidence-report`. Campaign paths below are relative to the public export.

## G1–G9

| Gate | Status | Evidence / remaining requirement |
|---|---|---|
| G1 Fresh bootstrap | **PASS** | September 28 clean candidate source archive plus recorded public overlays; fresh Go module/build caches, PostgreSQL 17.11 cluster/database/secret, generated fixtures and browser profiles. Migrations/repeat, enrollment/verification/scan/readiness and nested-file watcher convergence passed in `g1-final-*` campaigns. All resolved dependencies and direct-postgres sandbox fallback are public in the [bootstrap runbook](runbooks/M1-7-bootstrap.md). Subsequent migration correction verified by actual CLI 10k upgrade and full regressions. |
| G2 Complete journey | **NOT PERFORMED** | Desktop real-API journey passes. Final fresh corpus/database/profiles: `m17-final-chrome-closure/20260928T182216751Z-attempt-01`, M0 3/3, configured M1.4–M1.7 15/15, zero failure/skip/flaky. Physical same-LAN phone evidence still requires the user. |
| G3 Restart/crash recovery | **PASS** | Discovery process kill, late publication kill/backend termination, committed publication with withheld result, dirty-work hard crash/startup sweep, late natural-end backend termination/rollback/original-key retry, normal shutdown with opened throttled stream, durable state and lost-response replay. `g3-crash-campaign/20260928T142118912Z-attempt-01`, `g3-session-backend-interruption/20260928T144135504Z-attempt-01`, `g3-shutdown-final-correction/20260928T145248734Z-attempt-01`; rerun in final serial suite. |
| G4 Backup/restore | **PASS** | September 28 custom-format backup to fresh database; normalized schema, all 19 persistent tables/227 rows and sequence state equal before startup. Then readiness/catalog/occurrences/favorites/history/watcher/indexed Range/privacy passed. `g4-full-database-dump`, `g4-restore-full-db`, `g4-prestartup-compare`, `g4-restored-app-functional`. Same-host restore does not prove another host's media/root continuity. |
| G5 Historical upgrade/incompatibility | **PASS** | Faithful populated versions 2/4/5/6/7 → 8 preserve identity; damaged ledger/index/singleton/root fence/grouping rejected. Actual configured startup rejects unreachable/damaged/pending states. Partial-DDL and grouping-backfill backend interruption rollback/retry pass; actual v5 10k CLI upgrade passes with unchanged digest and 10k memberships. `g5-scale-and-interruption-closure/20260928T144724436Z-attempt-01`, final serial suite. Historical fixtures reconstructed from retained migrations, not archived production dumps. |
| G6 Failure/security/operations | **NOT PERFORMED** | All runnable route canaries, malformed input, Windows device/ADS/traversal/junction, permission/cancellation/root identity, disappearance/return/replacement and PostgreSQL outage/recovery pass. `g6-public-canary-matrix/20260928T144226662Z-attempt-01`, `g6-physical-root-operations/20260928T150817470Z-attempt-01`, final serial suite. Two Windows file-symlink confinement cases remain privilege-blocked. |
| G7 Performance | **PASS** | 30 changed-media scans at 10k scale with real catalog/Range/queue HTTP overlap in discovery and observed publication in every sample; independent percentile/interval audit; 30 real media watcher changes, 30 playlist-cap samples and all-10k Chrome decoding. Exact results below; no SLO. |
| G8 Regressions | **NOT PERFORMED** | Format, both vet commands, uncached unit, final serial PostgreSQL, tooling syntax and Chrome pass. Tagged suite: 237 pass/0 fail/8 skip; six optional benchmarks completed separately, two applicable symlink tests unexecuted. Unit suite: 100 pass, same applicable root-package symlink skip. |
| G9 Documentation/public evidence/scope | **PASS** | Public runbooks, candidate fingerprint, preserved failures, dated gates, sanitized exports with hashes and independent privacy/JSON validation. Public benchmark audit reproduces private metrics. Private plans/raw attempts remain ignored/untracked. Only production change is the measured foreground migration deadline; no M2+ feature. This gate truthfully records incomplete G2/G6/G8. |

## Demonstrated correction

Two actual populated v5 10k CLI migrations exceeded the shared 10-second context. Migrations 1–8 committed, grouping stayed incomplete, readiness rejected the database and identities remained stable. Failures remain in `g5-scale-production-migration`. Only `-migrate-only` now has a five-minute foreground context; normal configured startup retains its ten-second dependency budget. No migration/API/accepted architecture changed.

The corrected actual CLI upgrade took **16,488.463 ms**, preserved its identity digest, produced 10,000 memberships and passed readiness. `TestM17MigrationCLIAtTenThousandTracks` exercises the compiled production CLI; interrupted DDL/grouping tests prove rollback/retry. Evidence interval arithmetic now rejects zero/reversed intervals, guarded by `TestM17MeasuredIntervalOverlap`; no raw measurement was edited.

## Exact final regression results

| Check | Result / immutable attempt |
|---|---|
| `gofmt` | PASS `m17-final-format/20260928T151730005Z-attempt-01` |
| `go vet ./...` | PASS `m17-final-vet/20260928T151737161Z-attempt-01` |
| `go vet -tags=integration ./...` | PASS `m17-final-vet-integration/20260928T151743996Z-attempt-01` |
| `go test -json -count=1 ./...` | 100 test passes, one applicable symlink skip; `m17-final-unit/20260928T151750331Z-attempt-01` |
| `go test -json -p 1 -parallel 1 -tags=integration -count=1 -timeout 10m ./...` | **237 pass, 0 fail, 8 skip; 171,016.486 ms**. `m17-final-serial-integration/20260928T181604034Z-attempt-01` |
| PowerShell parse/Python AST/four JavaScript checks | PASS `m17-final-tool-syntax/20260928T183010120Z-attempt-01` |
| Chrome M0 | **3/3**, 6,790.170 ms, final browser attempt above |
| Chrome configured | **15/15**, 32,986.602 ms: M1.4 3, M1.5 10, M1.6 1, M1.7 1 |

M0 playback started after **504,806 of 26,460,044 bytes** transferred. Unbuffered seek generated `bytes=18513920-`, matched `206`/`Content-Range`, resumed playing in **183.700 ms**. Browser decoding/progress does not prove audible playback.

Serial coverage includes M0/M1.1/M1.2/M1.3A/M1.4, M1.5 receipts/CAS/selection/history and M1.6 coordinator/root fence. Six optional benchmarks (M1.5 storage, M1.6 Range/write and historical text-marker watcher, three new M1.7 campaigns) have separate completed evidence. Applicable skips: `TestReviewMediaSymlinkCannotEscape`, `TestWindowsKnownLocationReplacedBySymlinkIsDirectlyUnavailable`.

## Recovery/security observations

Publication and natural-end faults leave no half-committed catalog or listening/queue/receipt mutation. Lost committed scan delivery remains durable; next reconciliation hashes zero unchanged files. Abandoned discovery is finalized interrupted, and startup converges dirty/new files. Original-key natural-end retry completes one session and advances once.

Normal shutdown closes the unfinished throttled media response at the existing shutdown budget; it does not drain the entire 26 MB response. Queue/favorite/playlist/receipt state survives restart; a discarded successful queue response replays without another occurrence.

The real outage drill `g6-postgres-outage-recovery/20260928T024555477Z-attempt-01` completed an opened **26,460,044-byte** media response while PostgreSQL was down. Health 200; readiness/new durable operations typed 503; queue original-key replay, playlist/favorite/listening recovery passed. Later regression evidence retains the streaming contract.

Public UI/catalog/user-library/media routes were checked using canonical-path, relative-path, native-identity and credential canaries in headers/bodies/logs/receipts, with malformed/oversized JSON, IDs/cursors/tokens/receipt conflict, device/ADS/encoded traversal and Range cases. Root disappearance preserves state; matching return avoids rehash; replacement quarantines without absence; explicit verification permits later authority. Favorite/queue/history Track intent survives.

Host and restricted file-symlink probes failed. `CreateSymbolicLinkW` with unprivileged flag returned **Win32 1314**, token lacked `SeCreateSymbolicLinkPrivilege`. No settings changed. An elevated Administrator token with the privilege, or Developer Mode allowing unprivileged symlinks, is required. Both named tests must pass with zero skips; see [manual evidence](runbooks/M1-7-manual-evidence.md).

## Performance closure

Environment: Windows amd64/NTFS, 16 logical CPUs, Go 1.25.0, PostgreSQL 17.11, Chrome 153.0.8010.53, Playwright 1.55.1. Local caches, OneDrive/Windows activity and host load uncontrolled. Scale fixtures: **10,000 distinct MP3 whole-file hashes, 100 Artists/1,000 Albums, 44,037,871 bytes**, CC0-derived complete MPEG frames. Independent Chrome decoding **10,000/10,000, zero failures**. Realistic seeking uses separate 300-second WAV.

Final campaign has 10,001 Tracks/Objects/available locations including long WAV, queue 1,000 occurrences, 30 unique media retags. Each scan hashes/parses one changed file, observes expected grouping and reconciles absence through the accepted root fence.

| Workload | p50 / p95 / p99 (ms) |
|---|---|
| Changed-media reconciliation, 30 | **14,720.6455 / 42,292.931 / 44,804.962** |
| Scanner publication wrapper, 30 | **5,768.6235 / 35,009.028 / 37,487.584** |
| PostgreSQL sampled publication core, 30 | **1,159.1115 / 1,255.063 / 1,264.841** |
| Concurrent catalog, 6,284 | **67.3614 / 90.0549 / 267.3485** |
| Concurrent indexed Range, 34,868 positive intervals | **8.1286 / 15.4183 / 19.3433** |
| Concurrent 1,000-entry queue reorder, 1,022 | **449.1201 / 594.1419 / 726.2933** |
| Queue overlapping sampled publication, 93 | **429.1155 / 555.6096 / 726.2933** |
| Real changed-media fsnotify convergence, 30 | **892.408 / 914.114 / 1,006.416** |
| Supported 5,000-entry playlist reorder, 30 | **915.5995 / 1,020.137 / 1,824.395** |

**42,177 requests recorded; 42,174 positive intervals.** Three Range requests have equal timestamps; retained and excluded from latency/overlap. Original summary overcounts these three; independent audit records corrections. Every sample still has all three kinds overlapping discovery and PostgreSQL-observed publication. Corrected scan overlaps: catalog 6,283/Range 34,865/queue 1,019; observed publication: 477/2,559/93.

Publication wrapper includes waiting/acquisition/work. Server-clock `pg_stat_activity`/relation-scoped lock sampling gives a **lower-bound core**, not exact full transaction/lock duration. Wrapper tails and queue p99 726 ms are visible responsiveness costs. Reorders stayed correct and Range continued; no correctness failure justified redesign or an invented SLO. Playlist-cap data repeats one Track.

DB size **23,017,139 → 140,908,211 bytes**; cluster WAL **1,377,601,800 bytes during import plus samples** (global delta, not per-operation attribution). Prior idle/active process samples remain separate; failed DB-counter sampler is not claimed passed.

Final campaigns: `g7-changed-media-concurrency-final/20260928T145749267Z-attempt-01`, `g7-independent-final-audit/20260928T151519997Z-attempt-01`, `g7-real-media-watcher-thirty/20260928T145326723Z-attempt-01`, `g7-playlist-supported-cap/20260928T150743342Z-attempt-01`, `g7-distinct-corpus-decoder-validation/20260928T151602085Z-attempt-01`. Public audit independently reproduces percentiles/overlaps after sanitization.

## Historical evidence and preserved failures

| Prior evidence | Classification |
|---|---|
| M0 original upper-median/weak seek | Superseded by corrected consumed-response/unbuffered-seek measurements. |
| M1.1 pre-hardening parser | Historical; later metadata/security contracts apply. |
| M1.2 approximately 577 ms import | Limited environment/reconstruction detail; no current scale claim. |
| M1.3A lifetime overlap | Superseded for concurrency claims by request intervals. |
| M1.4 corrected resolver/Range overlap | Retained for recorded workload; old timeout lacks raw failed request/server trace, cause unknown. |
| M1.5 pre-correction/repeated Track | Historical narrow workload; 970 samples/16 operations independently recalculated. Queue read/reorder p99 4.235/218.864 ms; 500-playlist reorder p99 111.803 ms. |
| M1.6 empty-root marker/one Track | Limited baseline; current 30 real media changes supersede marker convergence claims. |
| Earlier M1.7 measurements | Retained: unchanged scans 10,419.328/12,560.618/13,086.605 ms; imports 46.911/57.836 s; repeated-Track queue 311.836/353.102/354.295 ms. Distinct workload/cache state, no SLO. |

Every failed/partial/timed-out attempt is retained separately from success. Important failures include:

- Git safe-directory setup, restricted-token PostgreSQL startup and fresh npm certificate validation; documented fallbacks succeeded, installed Playwright used fresh profiles.
- Restore normalization, invalid URI test DSN and browser fixture/favorite state; corrected setups passed.
- Two demonstrated 10k migration deadline failures; corrected foreground budget passed.
- Crash/shutdown driver, canary fixture and benchmark fixture errors; corrected campaigns passed. Earlier client-observed 30-scan phases remain; server-clock run is authoritative.
- Strict audit rejected equal timestamps; corrected audit excludes them without changing originals.
- File-symlink privilege failure/skips remain incomplete.
- First final serial run `20260928T181032062Z-attempt-01` failed because PostgreSQL was already absent before first DB test. Cause unestablished; original logs archived, restart/preflight and full rerun passed. Not attributed to shutdown test or product defect.
- First final Chrome enrollment of old temporary root failed before browser tests; fresh explicit corpus/database passed. Syntax-tool executable path and Windows PowerShell quoting failures preceded the passing rerun.

## Public artifacts and remaining work

Exports never replace raw originals. SHA validation checks original and derivative hashes, JSON validity and path/credential/canary absence. Binary media/dumps/schema SQL are private exclusions. Preliminary snapshots remain preserved locally; final export excludes its active recorder to avoid self-reference, with completed sanitized run manifest supplied separately. Public benchmark audit verifies unchanged numerical evidence. Candidate source fingerprint and executable hash identify the corrected tree.

Private blueprint/roadmap/acceptance/milestone files and raw attempts remain ignored/untracked. Scope is fixtures/tests/tooling/docs and one measured foreground migration deadline correction. No search/transcoding/remote access/auth expansion/AI/recommendation/durable queue/broker/generic jobs/multi-node feature entered production.

Before M1 can be declared complete:

1. User performs the physical same-LAN phone journey on corrected build (G2).
2. Provide Windows file-symlink capability; both named tests pass with zero skips (G6/G8).
3. Incorporate those new immutable attempts into a new sanitized export and update gates.

Runnable checks pass. **Not ready to commit as M1.7 completion** while G2/G6/G8 are NOT PERFORMED. No commit or push.
