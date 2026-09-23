# M1.3A foreground reconciliation

M1.3A replaces M1.2's known-path shortcut with an authoritative foreground scan. It does not add watching, scheduling, durable jobs, or event sourcing. The approved design and identity/failure matrix are recorded in [ADR-005](../adr/ADR-005-m1-3a-authoritative-reconciliation.md).

## Apply migration 0005

Use the existing private PostgreSQL instance and protect the connection secret as described in [M1.1 PostgreSQL operations](M1-1-postgres.md). For a populated database, stop Resonance writes, take a verified backup, then run:

```powershell
$env:RESONANCE_DATABASE_URL = 'host=127.0.0.1 port=55432 user=resonance dbname=resonance_m1 sslmode=disable'
go run . -migrate-only
go run . -addr 0.0.0.0:8080 -media data/demo.wav
```

Migration 0005 is forward-only. It retains all existing IDs, backfills each location's observed size from its MediaObject, deterministically sets Track metadata provenance to the earliest existing location, and marks pre-existing non-running M1.2 scan rows `finished` while leaving their M1.3A authority flags false. Existing M1.2 locations have no trusted mtime, so their first M1.3A scan hashes them once. Existing rootless diagnostic/test locations are outside enrolled-root reconciliation.

## Run and read a scan

Enroll roots through the existing host-only `library add` command. Then run:

```powershell
go run . library scan <root-id>
```

The process acquires one global advisory lease and runs a full scan in the foreground. It does not hold a database transaction while enumerating, opening, hashing, or parsing files. The command result includes the run/root IDs, status, phase-derived authority flags, file/hash/metadata counts, location/object/Track counts, publish duration, SQL statement count, command-tag rows changed, and total duration. Structured scan logs contain logical IDs and error codes, not canonical host paths.

Known files whose stored size and nanosecond mtime match are not opened or hashed. Their entry metadata is rechecked after traversal. Hashed files are checked on the open handle and at their confined path. If a path's identity/type/size/mtime changed, its observation is invalidated and the scan cannot interpret absence.

`succeeded` means a complete stable traversal applied observations and reconciled absence. `partial` can mean either a complete traversal with malformed media (absence was reconciled) or an incomplete traversal (absence was suppressed). Read `traversal_complete`, `observations_applied`, and `absence_reconciled`; status alone does not identify authority. A canceled or failed scan publishes no observation changes unless the final transaction committed and the persisted markers confirm it.

MediaObjects remain immutable. Exact SHA-256 matches reuse the existing MediaObject and its Track. A location is a physical occurrence; SHA-only moves create a new occurrence, and the prior one becomes unavailable only after a complete scan. A unique native file identity with compatible birth evidence can preserve a location across a move and can preserve Track identity across changed bytes. This is supporting evidence only; without birth data it cannot preserve Track identity across changed bytes. It cannot prove that a same-native overwrite contains the same musical recording.

## Failure and recovery

- Cancellation records `canceled` when the database is available. It does not reconcile absence.
- An unavailable or replaced root publishes nothing and retains catalog state.
- Directory permission/disappearance, file permission, file disappearance, unstable files, directory mutation, and traversal limits suppress mass absence. Directly observed unreadable, malformed, unsupported, irregular, and symlink replacements may mark only their known location unavailable with an explicit reason.
- A crash during traversal discards the connection-local temporary observation table. The next scan marks the old run `failed/finished/interrupted` and starts over.
- A crash or database error during final publication rolls back Track, MediaObject, location, availability, and generation-fence changes together.
- A response-uncertain commit is resolved by reading the run and root generation markers. Do not replay a publish blindly.
- A complete later scan is the convergence mechanism after an incomplete scan. Portable directory traversal narrows mutation races but is not a filesystem snapshot.

## Verification

Run the complete M0/M1.1/M1.2 Go regression suite, then the real PostgreSQL integration suites serially:

```powershell
go test -count=1 ./...
go test -p 1 -tags=integration -count=1 ./internal/storage ./internal/library
```

The integration suite uses `RESONANCE_TEST_DATABASE_URL` and `PGPASSWORD` for a dedicated disposable test database. It tests populated migration/backfill and contract drift, the full identity matrix, optional/native identity ambiguity, changed bytes and metadata-source policy, cancellation, mutation/disappearance, database interruption, final-transaction rollback, and M1.2 confinement regressions. The destructive negative test imports 1,000 locations, removes 900, fails traversal, confirms all 1,000 remain available, then confirms a complete follow-up marks exactly 900 unavailable.

## Reproduce M1.3A benchmarks

Use a dedicated PostgreSQL test database. If it does not exist, create it once with the local PostgreSQL `createdb` utility; do not point integration tests at a personal catalog database. The benchmark creates and drops isolated schemas and never migrates the existing personal catalog. The PowerShell runner starts a temporary local server schema for the M0 Range workload and a separate integration-test schema for reconciliation:

```powershell
$env:PGPASSWORD = '<private password>'
$env:RESONANCE_TEST_DATABASE_URL = 'host=127.0.0.1 port=55432 user=resonance dbname=resonance_test sslmode=disable'
pwsh -NoProfile -File tools/m1-3a-benchmark.ps1 -Repetitions 10
```

The runner records raw scan samples and event timestamps in `docs/benchmarks/M1-3A-raw.json` and `M1-3A-scan-events.jsonl`, plus idle/concurrent Range samples and a machine/process/database summary. Workloads cover first M1.2-state baseline, unchanged warm scans, one-percent mtime-only and new-byte changes, metadata retags, mixed retags/replacements, renames with the provider and with identity disabled, duplicate additions, disappearance, incomplete absence suppression, and complete follow-up scans. Each repeated workload reports p50, p95, and p99. Scan records include files enumerated/hashed, bytes hashed, metadata extraction attempts, identity counters, publish transaction time, SQL statements, and DML rows affected. The runner also samples process peak working set and CPU, PostgreSQL size/WAL change, and idle/concurrent Range p50/p95/p99. The Range client warms up before the scan, then timestamps each random request; concurrent percentiles include only requests intersecting the scan event interval.

The first scan follows corpus generation and is separated from immediate warm repetitions. This host does not provide a safe, reproducible OS file-cache flush, so the report labels the first pass cache state as unverified rather than claiming a cold-cache measurement. Do not turn these measurements into latency targets.

M1.3B filesystem watching remains deferred until this foreground scan path is independently accepted.
