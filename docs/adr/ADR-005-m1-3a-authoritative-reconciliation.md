# ADR-005: M1.3A authoritative foreground reconciliation

## Context

M1.2 imports unseen root-relative paths, then treats a known path as unchanged forever. It cannot detect changed bytes, removals, or moves. Its per-file transactions also cannot make a completed scan an atomic authority boundary. M1.3A adds foreground reconciliation while preserving the one-process server, enrolled-root confinement, global scan lease, SHA-256 byte identity, and M0 Range path. M1.3B watching is explicitly deferred.

## Decision

Each `library scan <root-id>` is a scan generation. It acquires the existing global advisory lease, records a `discovering` run, loads an autocommit catalog snapshot, traverses the enrolled root through `os.Root`, and stages observations in a connection-local temporary table. No database transaction remains open during filesystem I/O, hashing, or metadata parsing. Hashed files are re-statted on the same handle and rechecked at their confined path after traversal; unchanged, unsupported, and irregular file observations receive the same final path recheck before the scan can claim traversal completeness. After stability checks, the run moves to `publishing`; one final transaction locks and revalidates the root, resolves observation identities, applies safe observations, optionally reconciles absence, records counters and authority flags, advances the successful-generation fence when appropriate, and commits atomically.

`Track` is musical/catalog identity, `MediaObject` is immutable encoded-byte identity, and each `MediaLocation` is a physical file occurrence. No identity is physically deleted. Exact SHA-256 equality reuses the existing object and its Track. Path or metadata equality alone never transfers identity. A complete, stable traversal may mark unseen locations unavailable; incomplete, canceled, failed, or root-lost scans never infer mass absence. Partial scans may apply directly observed safe updates without advancing the generation fence.

### Identity matrix

| Observation | Track / MediaObject | Location |
|---|---|---|
| Known path and unchanged size/mtime | Preserve without hashing | Preserve ID; mark observed |
| Changed stat, same SHA | Preserve; skip metadata parse | Preserve ID; refresh observations |
| Exact SHA at another path | Reuse object and its Track | Create a new location |
| Unique native move, unchanged SHA | Preserve | Preserve ID and update path |
| SHA-only move evidence | Reuse object and Track | Create new location; old location becomes unavailable only after authoritative traversal |
| Same native object, new SHA, strong continuity | Create immutable object on the old Track | Preserve location ID; update Track metadata only if this is its metadata source |
| New SHA already belongs to another Track | Existing object's Track wins | Old occurrence becomes unavailable; create a new location |
| Path replacement without strong native continuity | Reuse by SHA if exact; otherwise create a new Track/object | Retire old occurrence and create a new location |
| Same metadata but different bytes, without continuity evidence | Create distinct Track/object | Create new location |
| Missing after complete stable traversal | Retain Track/object | Mark unavailable |
| Previously unavailable bytes return | Reuse Track/object by SHA only | Create a new location ID |

Native IDs are scoped evidence only. A move match requires the same root, provider and volume scope, file ID, compatible birth token, availability in the immediately previous authoritative generation, and exactly one old and one current candidate. Ambiguous IDs, hard links, ID reuse, unavailable history, or an observation gap disable native move inference. Without a birth token, native evidence may retain a location across a move only when SHA is unchanged; it cannot preserve Track identity across changed bytes. Even strong evidence cannot prove that in-place audio is musically the same recording; this is a documented continuity tradeoff.

Track metadata has deterministic source provenance. A new Track points at its first location; a proven continuous edit updates shared Track metadata only when the edited location is that source. A duplicate location cannot silently take over metadata precedence, and an unavailable source keeps its last metadata.

### State and failure semantics

Runs persist `discovering`, `publishing`, and `finished` phases plus independent `traversal_complete`, `observations_applied`, and `absence_reconciled` flags. A partial run can have complete traversal and reconciled absence (for example, malformed media) or incomplete traversal without absence reconciliation (for example, permission or mutation errors). Starting under the global lease marks prior `running` runs `failed/finished/interrupted`; their temporary observations are discarded.

- Cancellation records `canceled` when PostgreSQL is available and publishes no observations or absence.
- Root unavailable at start or lost during traversal retains all prior location state and publishes nothing.
- Directory permission/disappearance, file permission, file disappearance, unstable files, directory mutation, cancellation, or traversal limits prevent absence reconciliation. A known unreadable file may be marked unavailable as `unreadable`; a transiently disappearing file is not treated as a removal.
- Unsupported or malformed media does not itself make enumeration incomplete. A known location replaced by a symlink, irregular entry, unsupported format, or invalid content is marked unavailable with an explicit reason; invalid content never creates a MediaObject.
- Database failure before publish makes no catalog changes. A failed final transaction rolls back all catalog and absence changes. A commit with an uncertain response is resolved by reading the run and root generation markers, never by blind replay.
- A portable traversal is not a filesystem snapshot. Directory and file fences narrow mutation races; the next authoritative scan is the convergence mechanism.

## Alternatives considered

- Per-file persistent writes: rejected because a later incomplete traversal could leave partial catalog changes and cannot atomically apply the authority decision.
- Durable observation/event tables: rejected because scan staging is temporary work, not a durable event log or resumable job system.
- Path or metadata-based identity transfer: rejected because both are mutable and ambiguous.
- Mandatory native identity: rejected because it is platform-specific and unnecessary for SHA-based correctness.
- Watchers, scheduling, or generic jobs: deferred to M1.3B or later; a foreground scan is sufficient for this milestone.

## Consequences

Migration 0005 adds availability and observation evidence to locations, scan phase/authority/counters, root generation fencing, and Track metadata provenance. A partial unique path index permits historical unavailable occurrences while preventing duplicate active root-relative paths. Native identity is captured from already-confined open handles. First reconciliation of migrated M1.2 locations hashes them once because no trustworthy mtime observation exists. M1.2 legacy locations without roots remain outside root reconciliation. Historical non-running M1.2 scan rows are backfilled to `phase='finished'`; their authority flags remain false because those older scans did not establish a reconciliation generation.

Scans remain globally serialized. The final publish may hold a database transaction while applying many staged observations, but never while touching the filesystem. Exact-byte identity remains the only general Track reuse rule; strong native continuity is the narrow exception for changed bytes and carries an unavoidable musical-identity limitation.

## Evidence

Acceptance requires populated migration and schema-contract tests; assertions over Track, MediaObject, MediaLocation IDs, availability, authority flags, hash and metadata-extraction counts; regression coverage for the complete reconciliation matrix; destructive incomplete-traversal tests over at least 1,000 locations; all M0, M1.1, and M1.2 regressions; and reproducible reconciliation and Range-overlap benchmark artifacts that record first-pass/warm cache conditions, p50/p95/p99, and raw results. A cold-cache claim requires an explicitly verified safe cache-reset method.

Implementation evidence — 2026-09-23: migration 0005 passed populated backfill, historical-run phase backfill, atomic failure/retry, and schema drift tests. The PostgreSQL/filesystem integration suite covers the matrix, crash/interruption recovery, final-transaction rollback, Windows handle identity, hard-link ambiguity, path replacement after file open, and confinement regressions. The destructive negative test retained all 1,000 locations through an incomplete traversal after 900 deletions, then marked exactly 900 unavailable on a complete follow-up. `go fmt ./...`, `go vet ./...`, `go test -count=1 ./...`, serial storage/library integration tests, and all three M0 Playwright tests passed. Reconciliation and M0 Range p50/p95/p99 plus timestamped raw results are in `docs/benchmarks/M1-3A.md` and its linked artifacts. The host could not safely evict the OS file cache, so the first pass is recorded separately with cold-cache status unverified; WMI/CIM denied access to the physical disk model.

## What would cause reversal

Measured scan/publication cost, a concrete correctness counterexample to the generation fence, or user intent requiring a different same-native changed-byte policy would justify a new ADR. Watchers may only be added after this foreground scan is independently authoritative, idempotent, and accepted.
