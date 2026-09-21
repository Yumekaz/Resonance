# ADR-003: M1.1 persistence foundations

## Context

M1.1 needs durable catalog identities and a database readiness contract before enrollment or scanning. The approved persistence choice is PostgreSQL. The existing M0 diagnostic stream must remain independent of catalog readiness.

## Decision

Keep one Go server with pgx v5.11 and explicit SQL. The pinned driver requires Go 1.25. Its version is outside the affected range of [GHSA-9jj7-4m8r-rfcm](https://github.com/advisories/GHSA-9jj7-4m8r-rfcm); this is not a claim that dependency vulnerability checking is exhaustive.

Numbered SQL migrations are embedded and SHA-256 checksummed byte-for-byte. Migration SQL is pinned to LF through .gitattributes. Apply each migration and its ledger insertion in one transaction. Serialize migrators with a PostgreSQL session advisory lock on a dedicated physical connection; close that connection on all exits with bounded cleanup so locks cannot return to the pool. Readiness takes a shared transaction-scoped advisory lock and reports not-ready while a migrator holds the exclusive lock.

Normal startup does not migrate. It validates ledger ordering, names, checksums, the complete known migration set, and the expected columns/types/nullability/defaults, identity constraints, and lookup indexes of this binary's four tables. This explicit compatibility contract must evolve with migrations; it is not a general database forensic audit. Unknown, gapped, changed, pending, or structurally damaged schemas are rejected. A configured but unavailable/incompatible database prevents startup. With configuration omitted, the M0 diagnostic server starts and readiness is unconfigured. After startup an outage makes /ready return 503 while /health and the M0 stream remain available.

Tracks, encoded media objects, and physical locations are separate tables. One track can own multiple media objects; multiple locations can reference one media object. Encoded SHA-256 is globally unique. The current schema assigns each object to one track; it does not solve multi-release recording identity. Local paths are private opaque storage fields, not enrolled roots or authorization. No folder/scanner/catalog endpoints consume them in M1.1. Root scoping and path uniqueness must be decided when enrollment is actually implemented.

## Alternatives considered

- ORM auto-migration: hides the SQL and historical contract being tested.
- Migration on ordinary startup: changes state during process start and complicates recovery.
- Ledger-only validation: independent tests showed it accepts missing columns, constraints, and indexes.
- Returning a session-lock connection to the pool: risks retaining a lock if cleanup fails; migration frequency does not justify reusing that connection.
- Separate permanent application services or parser processes: no current requirement justifies them.

## Consequences

PostgreSQL is an operational dependency with setup, credential, connection, disk, backup, and upgrade costs. Migrations are forward-only. A failed migration rolls back its own work while prior committed versions remain applied. After fixing the cause, rerun migration; restore a verified compatible backup for broader recovery. Do not edit the ledger to suppress incompatibility.

The application pool is capped at four connections. Startup/listener failures return through the application function so pool-close defers run before fatal reporting. The M0 handler does not consult database readiness. No M1.2 enrollment, scanning, watcher, or catalog UI is included.

## Evidence

Independent PostgreSQL 17.11 tests cover empty/populated upgrades, byte checksums, ordering, idempotence, unknown/gapped versions, structural damage, concurrent migrators, lock-wait cancellation, readiness during migration, failure after a successful DDL statement, identity constraints, and pool reconnection. The independent restore drill compared all seeded values and ledger records, then passed normal startup/readiness. Deliberately dropping a restored column made normal startup fail despite unchanged ledger count. A real PostgreSQL stop/restart produced /ready 200 → 503 → 200 while health stayed 200 and demo ranges stayed 206.

## What would cause reversal

Measured persistence/operations costs may justify revisiting PostgreSQL through a separate decision. Parser isolation requires evidence that admitted inputs cannot be adequately bounded with the selected dependency. Additional schema and services require their own scoped requirement; they are not introduced here.
