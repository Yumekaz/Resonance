# M1.6 filesystem reconciliation status

M1.6 uses filesystem notifications only to schedule scans. The existing scanner remains the only component that can publish catalog observations or mark unseen locations unavailable.

## Read-only status

`GET /api/v1/library/status` returns the process-local coordinator snapshot with a live catalog-readiness check for its `database_state` field. It contains watcher state, bounded counters, global-sweep state, and up to 256 root entries. Root entries use logical root IDs and report verification state, current watch coverage, dirty/retry state, the last scan result, whether absence was reconciled, and the last scan age. A lost watcher changes covered roots to degraded; a later complete watch-set refresh clears that code. It does not return enrolled paths, event paths, filenames, or native root identity bytes. An unconfigured M0 server returns `503` for this endpoint.

`GET /health` remains process liveness. `GET /ready` remains PostgreSQL, exact-schema, and catalog-readiness status. A watcher failure is visible in `/api/v1/library/status` but does not make an otherwise usable catalog unready. Database or schema failure makes `/ready` return `503` while liveness remains `200`.

## Root verification

Migration 0008 adds a persistent root identity fence. Existing enrolled roots become `unverified`; the migration does not infer identity from a stored pathname. Watcher, startup, periodic, retry, and manual `library scan` triggers cannot verify or rebind a root. A host operator must run:

```powershell
go run . library verify <root-id>
```

Verification opens the enrolled root through the confined filesystem mechanism and confirms the opened object still matches the pathname before storing identity evidence. `library enable <root-id>` enables only a root whose current object matches that stored identity. Running `library verify` is the explicit host-side rebind operation.

Every scan captures identity A from the opened confined root and checks it against the stored identity. After traversal, it reopens the enrolled path and checks identity B against both A and the stored fence, including a path-to-opened-object comparison. A definite mismatch or unsupported identity evidence prevents publication and conditionally quarantines the still-matching stored root. A transient permission or I/O error reading identity also prevents publication but retains the verified fence for retry. The prior catalog remains intact. Root unavailability and database failure retain dirty retry intent rather than reconciling absence.

## Recovery behavior

- Create, write, remove, rename, and duplicate notifications mark only the containing root dirty. Event paths are discarded after root attribution.
- Root work is debounced for 750 ms, with a 5-second maximum delay from first dirtiness. Events arriving during a scan retain a follow-up scan intent.
- One coordinator event loop owns watcher reads and one worker invokes the authoritative scanner. Dirty-root state is capped at 256 entries. Overflow, uncertain attribution, watcher loss, or adapter errors request a paged global sweep.
- Directory watches are non-recursive and capped at 4,096 directories. Symlinks and reparse points are not traversed. Incomplete watch coverage is reported and periodic reconciliation remains active.
- Startup schedules a sweep of enabled verified roots. Configuration refresh runs every 60 seconds; full reconciliation repeats every 30 minutes. Scan retry starts at 5 seconds and is capped at 5 minutes, with jitter. Watcher recreation starts at 1 second and is capped at 1 minute, with jitter.
- A complete scan clears dirty state only when the scanner reports `traversal_complete=true` and `absence_reconciled=true`, and no newer event arrived during that scan.

These timings and caps are operational defaults, not service-level objectives. Coordinator state is in memory; startup and periodic scans recover missed work after restart. No event or job table is added.

## Host operations

```powershell
go run . -migrate-only
go run . library list
go run . library verify <root-id>
go run . library enable <root-id>
go run . library scan <root-id>
```

`library list` omits filesystem paths and native identity evidence. The verify/enable/scan commands are host-side operations and are not HTTP endpoints.
