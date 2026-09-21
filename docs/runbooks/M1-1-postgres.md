# M1.1 PostgreSQL local operations

Supported/tested project profile: PostgreSQL 17.x; independent review used 17.11 on Windows with Go 1.25.0. Other PostgreSQL majors are unverified. PostgreSQL is a local dependency; Resonance remains one executable. These commands use PowerShell 7 and a disposable development cluster. Keep lasting clusters and secrets in a protected, unsynced directory outside OneDrive and outside source control. Gitignore is not an access-control or backup policy.

## Clean local setup

Obtain PostgreSQL 17 Windows binaries from the [official download page](https://www.postgresql.org/download/windows/). Set pgBin to the installed distribution's bin directory. The helper below stops after native-command failures; PowerShell's ErrorActionPreference alone does not reliably make a failed native program terminate a script.

```powershell
$ErrorActionPreference = 'Stop'
$pgBin = 'C:\path\to\pgsql\bin'
function Assert-NativeSuccess([string]$operation) {
    if ($LASTEXITCODE -ne 0) { throw "$operation failed (exit $LASTEXITCODE)" }
}
if (Test-Path -LiteralPath data\pgcluster) { throw 'Cluster already exists; do not replace its password or initialize over it.' }
if (Test-Path -LiteralPath data\pgpassword.txt) { throw 'Password file already exists; use the existing setup or a new private directory.' }
New-Item -ItemType Directory -Force data | Out-Null
$bytes = [byte[]]::new(32)
[System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
[System.IO.File]::WriteAllText((Join-Path (Get-Location) 'data\pgpassword.txt'), [Convert]::ToBase64String($bytes))
& "$pgBin\initdb.exe" -D data\pgcluster -U resonance --auth-host=scram-sha-256 --auth-local=scram-sha-256 --pwfile=data\pgpassword.txt --encoding=UTF8
Assert-NativeSuccess 'initdb'
& "$pgBin\postgres.exe" -D data\pgcluster -p 55432 -h 127.0.0.1 -c max_connections=20
```

Keep PostgreSQL running in that terminal. In a second terminal, define the same Assert-NativeSuccess helper and pgBin, then:

```powershell
$pw = (Get-Content data\pgpassword.txt -Raw).Trim()
$env:PGPASSWORD = $pw
& "$pgBin\createdb.exe" -h 127.0.0.1 -p 55432 -U resonance resonance_m1
Assert-NativeSuccess 'createdb'
$env:RESONANCE_DATABASE_URL = "host=127.0.0.1 port=55432 user=resonance dbname=resonance_m1 password=$pw sslmode=disable"
go run . -migrate-only
Assert-NativeSuccess 'migrations'
go run ./cmd/fixture -out data/demo.wav -seconds 300
Assert-NativeSuccess 'fixture'
go run . -media data/demo.wav
```

This disposable setup uses the cluster owner; it is not a least-privilege deployment recipe. For a lasting instance use a non-superuser database owner with only the required schema privileges. Keep PostgreSQL bound to loopback, secure password/dump ACLs, and never print or commit a connection URL. Clear sensitive environment variables when finished. No remote access or authentication feature is introduced in M1.1.

## Health, readiness, startup and migrations

- /health is process liveness, not catalog availability.
- /ready checks connectivity, the exact migration ledger, and this binary's expected columns, types, nullability/defaults, identity constraints, and lookup indexes. It reports 503 while a migration owns the advisory lock.
- Without RESONANCE_DATABASE_URL, the M0 demo remains available and /ready says unconfigured.
- With configuration present, unreachable, pending, unknown, altered, or structurally incompatible schemas prevent startup. Runtime outages produce /ready 503 and recover when the database becomes compatible/reachable again. Health and demo streaming remain independent.
- Normal startup never migrates. Only -migrate-only mutates schema. Each numbered SQL file and ledger insertion commit together under a dedicated session advisory lock. Its connection closes on every exit. Migration SQL uses LF to keep checksums stable across checkouts.
- A failed later migration rolls back that migration, not previously committed versions. Fix the cause and retry. A lost connection around commit can leave the caller uncertain; retry checks the ledger before applying anything again.
- Stop application writes before an upgrade. Readiness is a point-in-time compatibility check, not a lifetime deployment lock. All migrators must use the application lock protocol; manual DDL can bypass advisory locks.
- Do not edit applied SQL or rewrite checksums to make errors disappear. There are no down migrations. Recover using a compatible executable plus a verified backup when a forward retry is unsuitable.

## Integration tests

Use a dedicated empty test database, never personal data. Integration tests create/drop randomly named schemas and terminate only their own test connection. Supply a role allowed to create schemas and manage its own sessions.

```powershell
$env:RESONANCE_TEST_DATABASE_URL = 'host=127.0.0.1 port=55432 user=resonance dbname=resonance_test sslmode=disable'
# Supply the password through PGPASSWORD or the private connection environment.
go test -tags=integration -count=1 -v ./internal/storage
Assert-NativeSuccess 'PostgreSQL integration suite'
go test -count=1 ./...
Assert-NativeSuccess 'Go regression suite'
```

The integration tag is deliberate: an ordinary go test does not establish PostgreSQL behavior. Missing test configuration is an error when integration tests are requested.

## Backup and restore drill

This is a metadata database backup, not a backup of the referenced audio. Dumps contain private location paths. Use protected storage. pg_dump uses a consistent database snapshot, but stop application writes during this comparison drill so the separately captured manifest describes the same data.

Use a fresh target database and a fresh dump name. Keep the original database untouched. Do not continue after any nonzero exit.

```powershell
$env:PGPASSWORD = (Get-Content data\pgpassword.txt -Raw).Trim()
if (Test-Path -LiteralPath data\m1-backup.dump) { throw 'Choose a fresh backup filename.' }
$manifestSQL = "SELECT jsonb_build_object('tracks',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tracks t),'media_objects',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM media_objects m),'media_locations',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM media_locations l),'migrations',(SELECT jsonb_agg(to_jsonb(s) ORDER BY version) FROM schema_migrations s));"
$sourceManifest = & "$pgBin\psql.exe" -h 127.0.0.1 -p 55432 -U resonance -d resonance_m1 -X -At -v ON_ERROR_STOP=1 -c $manifestSQL
Assert-NativeSuccess 'source manifest'
& "$pgBin\pg_dump.exe" -h 127.0.0.1 -p 55432 -U resonance -d resonance_m1 -Fc -f data\m1-backup.dump
Assert-NativeSuccess 'pg_dump'
& "$pgBin\createdb.exe" -h 127.0.0.1 -p 55432 -U resonance resonance_m1_restore
Assert-NativeSuccess 'create fresh restore target'
& "$pgBin\pg_restore.exe" -h 127.0.0.1 -p 55432 -U resonance -d resonance_m1_restore --exit-on-error --single-transaction --no-owner data\m1-backup.dump
Assert-NativeSuccess 'pg_restore'
$restoredManifest = & "$pgBin\psql.exe" -h 127.0.0.1 -p 55432 -U resonance -d resonance_m1_restore -X -At -v ON_ERROR_STOP=1 -c $manifestSQL
Assert-NativeSuccess 'restored manifest'
if ($sourceManifest -cne $restoredManifest) { throw 'Restored data or migration records differ.' }
$env:RESONANCE_DATABASE_URL = "host=127.0.0.1 port=55432 user=resonance dbname=resonance_m1_restore password=$env:PGPASSWORD sslmode=disable"
go run . -addr 127.0.0.1:18082 -media data/demo.wav
```

Normal startup must succeed; verify /ready is 200 on port 18082. Do NOT run migrations first to disguise an incompatible restore. A ledger count of two does not establish compatibility: the independent review demonstrated that a missing column, constraint, or required index can coexist with a correct ledger. The corrected application's structural checks reject these cases. Restore verification is not a full corruption, permission, trigger, or malicious-administrator audit.

A failed dump/restore must be retained as failed evidence or discarded only deliberately; do not promote it as a valid backup. For larger catalogs replace the in-memory JSON comparison with documented streaming checksums/representative queries. The above whole-row comparison is for the small M1.1 foundation corpus.

## Maintenance, upgrades and limits

The example caps PostgreSQL at 20 connections and the application pool at four. Migrations briefly use a dedicated connection. Budget storage for the cluster, WAL and dumps, monitor free space, and keep autovacuum enabled. VACUUM (ANALYZE) can refresh statistics after significant writes; no scanner exists here. Minor upgrades require a maintenance window, backup, server stop, binary replacement, restart, and readiness/data validation. Major upgrades require a separate pg_upgrade or dump/restore rehearsal; none was performed by M1.1. Read the [PostgreSQL 17 restore documentation](https://www.postgresql.org/docs/17/app-pgrestore.html).

## Independent evidence — 2026-09-20

A separate SCRAM-authenticated PostgreSQL 17.11 review cluster on loopback port 55433 was initialized from the local binary distribution. The existing developer cluster was not modified. Empty/populated migrations, concurrent migrators, canceled lock waits, atomic second-statement failure/retry, unknown/gapped/changed versions, identity constraints, structural damage, and reconnection were tested against the real database.

Two tracks (including Unicode and null title), two encoded objects, three physical locations, and both complete ledger records were dumped/restored and compared exactly, including IDs, hashes, nullable values, timestamps and checksums. A normal server started on the restored database and returned /ready 200. Deliberately removing tracks.title from that test restore caused normal startup to fail despite the unchanged ledger. This negative test is separate from the good backup.

A real database stop/restart changed readiness 200 → 503 → 200 while health stayed 200 and demo Range requests stayed 206 with exact 100-byte bodies. Configured unavailable/incompatible/invalid startup failed safely; an unconfigured server still served the M0 demo. No catalog enrollment, scanning, watching, or UI was added.
