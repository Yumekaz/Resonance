# M1.2 library enrollment and initial scan

M1.2 adds host-only folder enrollment and initial import. Complete the PostgreSQL setup and migration steps in `M1-1-postgres.md`, then apply all pending M1.2 migrations (3 and the review correction 4) with the same executable:

```powershell
$env:RESONANCE_DATABASE_URL = 'host=127.0.0.1 port=55432 user=resonance dbname=resonance_m1 password=<private> sslmode=disable'
go run . -migrate-only
```

Do not place database credentials in shell history, source files, logs, or screenshots. The examples below run locally on the host. There is no web enrollment API.

## Commands

```powershell
go run . library add -path 'D:\Music' -name 'Main library'
go run . library list
go run . library scan <root-id>
go run . library disable <root-id>
```

`add` returns the stable root UUID, display name, and enabled state. `list` returns the same public-safe fields and omits the canonical host path. Exact and nested root overlaps are rejected. A root must exist, be a readable directory, and resolve through filesystem links during enrollment. Disabling is persistent and does not remove catalog rows or media files.

`scan` runs in the foreground so Ctrl+C cancels it. Only one scan process is admitted at once. The final JSON result contains the run/root IDs, status, counters, hashed bytes, metadata extraction count, and duration. Structured JSON logs contain the same logical IDs and bounded error codes. They intentionally omit private absolute paths.

## Import behavior

- Supported media: MP3, FLAC, and PCM WAV. Extensions are only the first filter. Malformed files fail explicitly.
- MP3/FLAC: restricted M1.1 tag extraction for title, artist credit, album title, album artist, track/disc number, year, genre, and safe artwork reference. These raw observations are stored on Track; Artist/Album grouping identities are deferred to M1.4.
- WAV: bounded RIFF/PCM validation, with filename-derived title. No WAV tags or properties are fabricated.
- Missing title: filename without extension. Missing artist and album remain null; files are not merged into an “Unknown Album.” For exact duplicate untagged bytes at different filenames, the first imported fallback title remains on their shared Track; this is presentation metadata, not Track-ID derivation.
- Every newly observed location is streamed through SHA-256. Exact bytes reuse one MediaObject across locations.
- Scan writes one file at a time in short transactions. A corrupt or inaccessible file records a bounded error and does not discard earlier imports.
- Symbolic links, Windows junctions/reparse points, and irregular entries are skipped. The scanner never deletes or modifies media files.

M1.2 never marks an unseen location deleted. A repeated scan skips a known root-relative path without rehashing it. Replacements, moves, renames, removed files, and authoritative reconciliation are deliberately deferred to M1.3.

## Error and recovery behavior

Root unavailable or disabled errors stop that scan. Per-file errors use codes such as `permission_denied`, `file_unavailable`, `metadata_invalid`, `malformed_wav`, `unsupported_format`, and `symlink_skipped`. At most 100 error rows are retained per scan; aggregate counters continue beyond that cap.

A PostgreSQL error fails the scan. If the database is unavailable during finalization, the row may remain `running`; the next scanner that acquires the worker lock changes such rows to `failed`/`interrupted` before starting. Re-run the scan after PostgreSQL and `/ready` recover. Already committed files remain durable and known locations are skipped. This is restart recovery, not M1.3 reconciliation.

## Tests

Use a dedicated PostgreSQL test database as documented in the M1.1 runbook:

```powershell
go test -count=1 ./...
go test -p 1 -tags=integration -count=1 ./internal/storage ./internal/library
```

The `-p 1` setting keeps destructive database fault tests serial on this Windows test host. Tests create isolated PostgreSQL schemas and workspace-local temporary library directories.

## Benchmark

Create the controlled corpus and enroll it in a clean migrated test database:

```powershell
pwsh -NoProfile -File tools/m1-2-corpus.ps1 -OutputDir data/m1-2-benchmark-root -MP3Count 300
go run ./cmd/fixture -out data/m1-2-benchmark-root/tone.wav -seconds 1
go build -o data/m12-server.exe .
go build -o data/m12-bench.exe ./cmd/bench
data/m12-server.exe library add -path data/m1-2-benchmark-root -name 'M1.2 Benchmark'
```

Start `data/m12-server.exe -media data/demo.wav`, then run:

```powershell
$env:PGPASSWORD = '<private>'
pwsh -NoProfile -File tools/m1-2-benchmark.ps1 -RootId <root-id>
```

The script records raw scan telemetry, scanner CPU/memory samples, PostgreSQL size, and idle/concurrent M0 HTTP Range results. Use a fresh root/database for an initial-import measurement.
