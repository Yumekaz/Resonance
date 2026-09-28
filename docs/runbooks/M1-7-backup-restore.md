# M1 complete PostgreSQL backup and restore

This is a same-host metadata-database recovery drill. It does not back up original media bytes, extracted artwork bytes, browser playback state, process-local watcher intent, executable files, configuration, credentials, or PostgreSQL roles. Database dumps and raw manifests are private artifacts: they contain library locations, listening state and mutation receipts. Store them outside the repository and OneDrive with restrictive ACLs.

## Prepare a consistent source

Stop the Resonance server and every writer. Leave PostgreSQL available. Confirm no scanner or user-library mutation is in flight. Use a new dump name and a fresh restore database. Keep credentials in the process environment or a private password file; do not place a connection URL in command history.

The M1.7 verifier `go run ./cmd/m17manifest` emits a hash-only manifest for every public base table, every table's columns/constraints/indexes, and every public sequence's options and current `last_value`/`is_called`. It never emits row values or media paths. Compare this manifest before application startup; matching counts alone are insufficient.

```powershell
$ErrorActionPreference = 'Stop'
$pgBin = 'C:\Program Files\PostgreSQL\17\bin'
$backupDir = 'D:\Resonance-private-backups'
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$dump = Join-Path $backupDir "resonance-$stamp.backup"
$schema = Join-Path $backupDir "resonance-$stamp.schema.sql"
if (Test-Path -LiteralPath $dump) { throw 'Choose a new dump filename.' }
$env:RESONANCE_DATABASE_URL = 'host=127.0.0.1 port=55433 user=resonance dbname=resonance_m1 sslmode=disable'
$env:PGPASSWORD = (Get-Content 'D:\Resonance-private\postgres-password.txt' -Raw).Trim()
go run ./cmd/m17manifest | Set-Content (Join-Path $backupDir "$stamp.source-manifest.json") -Encoding utf8
if ($LASTEXITCODE -ne 0) { throw 'Source data manifest failed.' }
& "$pgBin\pg_dump.exe" -h 127.0.0.1 -p 55433 -U resonance -d resonance_m1 --schema-only --no-owner --no-privileges --file $schema
if ($LASTEXITCODE -ne 0) { throw 'Schema-only backup failed.' }
& "$pgBin\pg_dump.exe" -h 127.0.0.1 -p 55433 -U resonance -d resonance_m1 --format=custom --no-owner --no-privileges --file $dump
if ($LASTEXITCODE -ne 0) { throw 'Custom-format backup failed.' }
Get-FileHash -Algorithm SHA256 -LiteralPath $dump
& "$pgBin\pg_dump.exe" --version
if ($LASTEXITCODE -ne 0) { throw 'Could not record pg_dump version.' }
```

Record the dump checksum, size, tool version, start/end times and any failure. A backup manifest and schema dump must remain private even though the M1.7 verifier itself emits only hashes.

## Restore and compare before application startup

Create a new database, restore the custom dump in one transaction, then capture the restored manifest. Do not start Resonance or run migrations before comparison; that would conceal a restore mismatch.

```powershell
& "$pgBin\createdb.exe" -h 127.0.0.1 -p 55433 -U resonance resonance_restore
if ($LASTEXITCODE -ne 0) { throw 'Fresh restore database creation failed.' }
& "$pgBin\pg_restore.exe" --exit-on-error --single-transaction --no-owner --no-privileges -h 127.0.0.1 -p 55433 -U resonance -d resonance_restore $dump
if ($LASTEXITCODE -ne 0) { throw 'Restore failed.' }
$env:RESONANCE_DATABASE_URL = 'host=127.0.0.1 port=55433 user=resonance dbname=resonance_restore sslmode=disable'
go run ./cmd/m17manifest | Set-Content (Join-Path $backupDir "$stamp.restore-manifest.json") -Encoding utf8
if ($LASTEXITCODE -ne 0) { throw 'Restored data manifest failed.' }
& "$pgBin\pg_dump.exe" -h 127.0.0.1 -p 55433 -U resonance -d resonance_restore --schema-only --no-owner --no-privileges --file (Join-Path $backupDir "$stamp.restore.schema.sql")
if ($LASTEXITCODE -ne 0) { throw 'Restored schema dump failed.' }
```

Compare every table's row count and value hash, each table schema hash, every sequence option/state, and the normalized schema-only DDL. PostgreSQL adds a random `\restrict` token to each dump; normalize only that generated token before byte comparison. The schema fingerprint compares logical columns in ordinal order but ignores gaps left by dropped physical column slots; the schema-only dump still verifies the complete DDL.

## Start the restored database normally

Only after the pre-start comparison succeeds, start the accepted executable with the restore database URL and no migration mode. Verify `/ready`, actual Artist/Album/Track reads, a `206` indexed-media Range request, queue and playlist duplicates, favorite state, recent history, and an identity-verified root with active watcher coverage. The database restore does not establish that physical media or root identity continued; on another host or filesystem, inspect the path and explicitly verify/rebind roots before scanning.

No RPO or RTO is promised. Keep the original source database and media untouched until the restore checks complete. Clean up only disposable restore databases and clusters after preserving their attempt artifacts.
