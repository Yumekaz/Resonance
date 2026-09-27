# ADR-008 — M1.6 filesystem hints and authoritative convergence

## Status

Accepted for M1.6 implementation. M0–M1.5 catalog and media contracts remain in force.

## Context

M1.3A provides the only authority for interpreting filesystem absence: a complete stable traversal stages observations and publishes them atomically under a global scan lease. M1.6 needs lower-latency discovery and recovery without creating a second catalog mutation path. Filesystem notifications are lossy hints, roots may disappear or be rebound, and process-local work state may be lost on crash.

## Decision

Keep one Go server, PostgreSQL as metadata authority, one event reader, and one scan worker that invokes the existing `library.Scanner`. Watcher events never write catalog rows or enqueue paths. They set one bounded dirty bit and increment a per-root generation epoch. A scan clears dirty intent only after a committed run reports `traversal_complete=true` and `absence_reconciled=true`, and only if no event arrived after the scan's start epoch. Failure, partial authority, lease contention, root unavailability, or intervening events retain work intent and use bounded backoff.

Use the pinned `github.com/fsnotify/fsnotify` v1.10.1 adapter. Register directories, not media files. Refresh the safe directory watch set after scans; skip symlinks, junctions, and reparse points. If a directory or root cannot be watched, report degraded coverage and retain periodic reconciliation. Overflow, adapter errors, closed channels, full dirty-entry capacity, or uncertain root attribution request a whole-root/global sweep. Dirty state remains in memory; process start always sweeps all enabled verified roots, so no durable job or event table is needed.

Add migration 0008 with root identity kind, scope, ID, nullable birth evidence, verification state, and verification time. Existing roots migrate as `unverified`; migration does not fabricate identity. Enrollment captures identity from an opened confined root only when the platform can provide nonzero birth evidence as well as an object ID. A device/inode pair without birth evidence is too weak after a root disappears and can be reused, so it cannot authorize destructive absence. Host-only `library verify <root-id>` explicitly pins or rebinds the current root object. Watcher and automatic scan triggers cannot verify or rebind a root. `library enable <root-id>` enables only after the current path matches its pinned identity; disabled roots remain excluded until then.

Every absence-authoritative scan captures identity A from an opened confined root handle and requires A to match the persisted verified identity. It traverses only through that `os.Root`. After traversal, it reopens the enrolled pathname, captures identity B, and requires B to match A and the persisted identity. A definite mismatch or unsupported identity evidence conditionally quarantines the still-matching pinned root and finalizes the run without publishing observations or absence. A transient permission or I/O error while reading identity also suppresses all publication, but leaves the pin verified for bounded retry after recovery. The final PostgreSQL publication transaction locks and rechecks the root's enabled and verified state plus the expected identity fence. Weak or unavailable platform evidence cannot authorize destructive absence; it remains visibly unverified until a host-side verification can establish a supported fence. There remains a portable filesystem/SQL timing gap after the final path recheck; the next authoritative scan is the convergence mechanism if the path changes in that gap.

Startup refreshes root configuration, establishes watches where possible, and schedules an immediate sweep in the background. A staggered periodic full reconciliation runs every 30 minutes; host-side root changes are discovered by a 60-second configuration refresh. Events debounce for 750 ms, with a 5-second maximum delay from first dirtiness and a minimum 750 ms between follow-up scans. Scan retry starts at 5 seconds with exponential backoff capped at 5 minutes and jitter. Watcher recreation starts at 1 second and caps at 1 minute. Dirty-root entries are capped at 256; overflow becomes a global sweep cursor. Watched directories are capped at 4,096; exceeding the cap is reported as degraded and relies on periodic scans. These are operational defaults, not SLOs.

`/health` remains process liveness. `/ready` continues to reflect PostgreSQL, schema, and catalog readiness, not watcher success; a failed watcher does not disable cached browsing or an already-open Range stream. A read-only `/api/v1/library/status` reports root IDs, verification/watch/dirty/retry states, last scan authority and age, bounded counters, and watcher health. Logs and diagnostics use root ID, scan run ID, coordinator cycle ID, trigger and error code; they never include event paths.

Shutdown stops admitting work, cancels the active scan, closes watcher resources, shuts down HTTP, and waits a bounded interval for scan finalization. M1.3A continues to mark an interrupted run and discard its connection-local observations on the next start.

## Alternatives considered

- Mutating catalog state directly from notifications: rejected because event order and coverage are not authoritative.
- Durable event or job queues: rejected because startup and periodic sweeps recover disposable dirty state.
- Per-file watch queues: rejected because bounded root dirtiness collapses storms and needs no path-bearing queue.
- Treating path equality or one open handle as root identity: rejected because the path may be rebound while the original handle remains valid.
- Returning readiness 503 on watcher failure: rejected because watcher availability is not required for cached catalog reads or already-open media streams.
- Kubernetes, brokers, Redis, Kafka, search, transcoding, remote access, recommendation, or multi-node behavior: out of scope.

## Consequences

Missed notifications converge after startup or periodic reconciliation. Watch coverage is best effort and always reported. Existing 0007 roots require a host verification step before authoritative absence reconciliation. A root identity mismatch preserves catalog state and requires explicit rebind. The scan transaction, generation fence, Track/MediaObject/MediaLocation identities, user-library Track references, and M0/M1.4 Range path remain authoritative and unchanged.

## Evidence required

Acceptance requires populated and empty 0007→0008 migration tests, interrupted migration retry, exact schema/readiness drift checks, root verification and start/end path-rebind fault tests, deterministic fake-notifier state-machine tests, real Windows NTFS watcher and junction tests, bounded event storms and overflow recovery, root disable/enable and PostgreSQL outage recovery, M0–M1.5 regressions, and reproducible convergence/Range benchmarks with request-level overlap timestamps. Evidence must report actual p50/p95/p99 and limitations without inventing an SLO.
