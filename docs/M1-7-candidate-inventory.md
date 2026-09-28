# M1.7 candidate and evidence inventory

Inventory time: 2026-09-28 Asia/Calcutta. Execution stage 1 of the approved M1.7 plan. No M1.7 campaign had started when this inventory was written.

## Candidate

- Candidate commit/tag: `47c31c5e55ff89edffaa55a037762ce85496f63b` / `m1.6-watcher-recovery`.
- Branch: `main`; the tracked working tree was clean at inventory time.
- The private blueprint, roadmap, milestone and acceptance files are ignored by `.gitignore`; they are inputs, not public release artifacts.
- The private acceptance ledger has M1.7 unchecked. It also retains unchecked M1.5 entries; this campaign will not silently rewrite that prior record.

## Existing evidence and remaining scope

| Area | Existing evidence | M1.7 gap/treatment |
|---|---|---|
| M0 | Generated WAV fixture, Range tests, three Chrome player tests and local benchmark artifacts. | The M0 evidence is historical and does not prove a physical M1.7 phone journey. |
| M1.1–M1.3A | Real-PostgreSQL migrations, scanner identity/reconciliation faults, populated upgrades, schema/readiness checks and raw scan/Range reports. | Existing restore manifest covers only the original core tables. Expand it to all persistent M1 tables. |
| M1.4 | Catalog and indexed playback tests, 304-Track query results and prior physical phone evidence. | A 304-Track Tracks-page timeout is documented, but its failed raw request/server trace is absent. The successful rerun is not proof of the failed attempt's cause. No 10k baseline exists. |
| M1.5 | Queue, playlist, favorites, history, retries, concurrency tests and browser suite. Raw warm-cache timings exist. | One-Track queue/playlist workloads remain limited; a physical M1.5 phone-controls run was not performed. |
| M1.6 | Root identity fence, migration 0008, real Windows watcher/junction tests, live PostgreSQL outage/recovery, coordinator convergence and local p50/p95/p99 reports. | Existing convergence data is an empty-root text-marker workload; Range overlap uses a small local WAV. Sustained idle resources are unmeasured. |
| Browser | M0, M1.4, M1.5 and M1.6 Playwright suites exist. | Some M1.4/M1.5 tests intentionally intercept catalog responses. Add one complete M1.7 journey using actual catalog responses and fresh browser state. |
| Operations | PostgreSQL and library runbooks exist. | Public bootstrap is split between an M0 quickstart and version-specific runbooks; reconcile into a clean source-to-ready workflow. |

## Persistent-data inventory for G4

Migrations 0001–0008 create the migration ledger; Tracks, MediaObjects and MediaLocations; library roots, scan runs and scan errors; grouping state, Artists, Albums and membership tables; active queue/items; playlists/items; favorites; playback sessions; and mutation receipts. Backup manifests must compare every persistent table, relevant sequences and schema definitions. Original media, extracted artwork bytes, in-memory watcher intent and browser playback state are outside the PostgreSQL restore set.

## Bootstrap dependencies observed before execution

- `go`, `postgres`, `psql`, `initdb` and `pg_ctl` were not on `PATH`; no system Go or PostgreSQL installation was found at the standard checked paths. Go 1.25 is present in a user module cache, while the repository's ignored `data/` also contains a Go SDK/cache and PostgreSQL binaries/clusters.
- Node.js/npm and Chrome are globally available. The repository has ignored `node_modules`, browser output, a generated M1.4 browser root, a demo WAV and local database clusters.
- Existing Resonance/PostgreSQL environment variable names were absent at inventory time. Secret values are not recorded.
- None of the ignored databases, media, browser state or repository-local Go SDK may be used to claim G1. G1 must use a fresh source tree, newly created PostgreSQL cluster/database, newly generated media, empty browser profile and explicit toolchain/PATH evidence. If this host cannot establish that independence, G1 stays NOT PERFORMED.

## Attempt-preservation protocol

- No M1.7 campaign attempt had run at inventory time.
- Every M1.7 run gets a unique timestamped attempt directory with command, candidate fingerprint, start/end, status, exit code and raw logs/samples. Failed, timed-out and partial attempts are never overwritten by reruns.
- Existing benchmark JSON/JSONL is immutable input. The M1.4 timeout noted above has no retained raw failed request; it will be reported as a missing historical artifact, not reconstructed or replaced with success data.

## Initial gate state

G1–G9 are **NOT PERFORMED** at inventory time. Existing M0–M1.6 evidence will count only where its workload and setup match the approved M1.7 gate.

## Execution setup update — 2026-09-28

- G1 used a new source archive of candidate `47c31c5e55ff89edffaa55a037762ce85496f63b` plus the explicitly SHA-256-listed public overlays in the temporary source-snapshot manifest. The checkout contained no `.git`, `data`, `node_modules`, `.env`, generated library, private plan files, or existing database.
- Go 1.25.0 was resolved explicitly from the user-level Go module cache because no system Go was on `PATH`; `GOROOT`, `PATH`, `GOTOOLCHAIN=local`, `GOPATH`, `GOMODCACHE`, and `GOCACHE` were set explicitly. Module downloads populated a fresh temporary cache. No repository-local Go SDK or prior module cache was used for G1.
- PostgreSQL 17.11 binaries were explicitly resolved from the ignored portable distribution available under repository `data/`; the test used a newly initialized cluster and randomly generated SCRAM secret under the temporary directory. No existing PostgreSQL cluster, database, password, or cluster configuration was used. `pg_ctl start` failed with Windows `CreateRestrictedToken` error 87 in this sandbox; the public bootstrap runbook documents the direct `postgres.exe` fallback used for the fresh cluster. This is a host/tooling provenance limitation, not an application behavior change.
- The G1 server's initial scan imported 9 journey Tracks, 9 MediaObjects and 10 locations; readiness, catalog browsing and watcher coverage were verified. A new nested tagged Track was published through the real API about 1.2 seconds after it was added, and the root returned to clean, watching state.
- A fresh `npm ci` attempt in the source archive failed because npm could not verify the registry certificate. That failure remains separately recorded. G2 used the already-installed Playwright package, an explicit installed Chrome path, and Playwright's fresh per-test browser profile; it did not use prior browser cookies or session state.
