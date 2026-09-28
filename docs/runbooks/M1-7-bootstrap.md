# M1 clean Windows bootstrap

This runbook builds and starts the current M1 single-node product from a clean source checkout using a fresh PostgreSQL cluster and freshly generated media. The commands are for Windows PowerShell 7. Use a local, access-controlled directory outside the source checkout and outside OneDrive for PostgreSQL data, generated media, module caches, logs and secrets. Do not point these steps at a personal database or library.

## Prerequisites

- Windows x64, Go 1.25.x, Git, and PostgreSQL 17.x server utilities (`initdb`, `pg_ctl`, `createdb`, `psql`). Record exact patch versions for a verification report.
- Network access to the Go module proxy for the first build, or an explicitly provisioned dependency cache whose provenance is recorded.
- Node.js 18+ and Chrome are needed only for the browser suites. A phone journey additionally requires a physical phone on the same trusted LAN.

Run PowerShell with `-NoProfile`. Set the Go and PostgreSQL paths explicitly; this runbook does not rely on repository-local SDKs, repository data, a global PostgreSQL service, a user-specific database, a pre-generated fixture, or a browser profile. A fresh module cache is used below.

## Initialize an isolated PostgreSQL cluster

Choose a new private directory and port. The script refuses an existing root directory, generates a fresh password, and binds PostgreSQL to loopback only.

```powershell
$ErrorActionPreference = 'Stop'
$work = 'D:\Resonance-M1-bootstrap'
$goBin = 'C:\Program Files\Go\bin'
$pgBin = 'C:\Program Files\PostgreSQL\17\bin'
$port = 55433
if (Test-Path -LiteralPath $work) { throw 'Choose a new empty private work directory.' }
New-Item -ItemType Directory -Path $work | Out-Null
$env:PATH = "$goBin;$pgBin;$env:SystemRoot\System32"
$env:GOTOOLCHAIN = 'local'
$env:GOPATH = Join-Path $work 'gopath'
$env:GOMODCACHE = Join-Path $env:GOPATH 'pkg\mod'
$env:GOCACHE = Join-Path $work 'gocache'
Get-Command go, initdb, pg_ctl, createdb, psql | Select-Object Name, Source
go version
psql --version
$cluster = Join-Path $work 'postgres-data'
$password = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$passwordFile = Join-Path $work 'postgres-password.txt'
[IO.File]::WriteAllText($passwordFile, $password + [Environment]::NewLine)
initdb -D $cluster -U resonance --auth-host=scram-sha-256 --auth-local=scram-sha-256 --pwfile=$passwordFile --encoding=UTF8
if ($LASTEXITCODE -ne 0) { throw "initdb failed: $LASTEXITCODE" }
pg_ctl -D $cluster -l (Join-Path $work 'postgres.log') -o "-p $port -h 127.0.0.1 -c max_connections=30" -w start
if ($LASTEXITCODE -ne 0) { throw "pg_ctl start failed: $LASTEXITCODE" }
$env:PGPASSWORD = $password
createdb -h 127.0.0.1 -p $port -U resonance resonance_m1
if ($LASTEXITCODE -ne 0) { throw "createdb failed: $LASTEXITCODE" }
$env:RESONANCE_DATABASE_URL = "host=127.0.0.1 port=$port user=resonance dbname=resonance_m1 password=$password sslmode=disable"
```

In a restricted Windows test runner, `pg_ctl start` can fail with `could not create restricted token: error code 87`. This is a process-launch limitation, not a database or Resonance result. Use this explicit fallback with the same new cluster, port, and loopback binding, then confirm it is ready before creating the database:

```powershell
$postgres = Start-Process -FilePath (Join-Path $pgBin 'postgres.exe') `
  -ArgumentList @('-D', $cluster, '-p', "$port", '-h', '127.0.0.1', '-c', 'max_connections=30') `
  -WindowStyle Hidden -RedirectStandardOutput (Join-Path $work 'postgres.stdout.log') `
  -RedirectStandardError (Join-Path $work 'postgres.stderr.log') -PassThru
pg_isready -h 127.0.0.1 -p $port -U resonance
if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL did not become ready' }
```

Record the PID and server logs. In a normal Windows installation, prefer `pg_ctl` for start and fast shutdown. A restricted runner may use `Stop-Process -Id $postgres.Id` only to clean up its own disposable server after the test; that forced stop is not a clean-shutdown test.

Keep the password file private. Do not print the database URL. For a persistent deployment, create a least-privilege database owner and follow the PostgreSQL runbook; this disposable single-user cluster is a test bootstrap, not a production hardening recipe.

## Build, migrate, enroll, scan and start

From a clean checkout of the candidate source, use the public CC0 parser samples to generate new journey media outside the repository. The generator refuses to overwrite an existing output directory. Run migrations twice to verify idempotent startup state.

```powershell
Set-Location 'C:\src\RESONANCE'
go mod download
if ($LASTEXITCODE -ne 0) { throw "go mod download failed: $LASTEXITCODE" }
go run . -migrate-only
if ($LASTEXITCODE -ne 0) { throw 'initial migrations failed' }
go run . -migrate-only
if ($LASTEXITCODE -ne 0) { throw 'repeat migrations failed' }
$journey = Join-Path $work 'journey-corpus'
go run ./cmd/m17corpus -mode journey -out $journey
if ($LASTEXITCODE -ne 0) { throw 'journey corpus generation failed' }
$libraryPath = Join-Path $journey 'library'
$root = go run . library add -path $libraryPath -name 'M1 journey'
if ($LASTEXITCODE -ne 0) { throw 'root enrollment failed' }
$rootId = ($root | ConvertFrom-Json).id
go run . library verify $rootId
if ($LASTEXITCODE -ne 0) { throw 'root identity verification failed' }
go run . library enable $rootId
if ($LASTEXITCODE -ne 0) { throw 'root enable failed' }
go run . library scan $rootId
if ($LASTEXITCODE -ne 0) { throw 'initial authoritative scan failed' }
$media = Join-Path $libraryPath 'long.wav'
go run . -addr 127.0.0.1:8080 -media $media
```

In a second PowerShell window, set the same explicit paths and load the private password locally; do not paste it into a command line or log. Confirm readiness and that catalog browsing returns imported groups:

```powershell
$work = 'D:\Resonance-M1-bootstrap'
$goBin = 'C:\Program Files\Go\bin'
$pgBin = 'C:\Program Files\PostgreSQL\17\bin'
$port = 55433
$env:PATH = "$goBin;$pgBin;$env:SystemRoot\System32"
$env:GOTOOLCHAIN = 'local'
$env:GOPATH = Join-Path $work 'gopath'
$env:GOMODCACHE = Join-Path $env:GOPATH 'pkg\mod'
$env:GOCACHE = Join-Path $work 'gocache'
$password = (Get-Content (Join-Path $work 'postgres-password.txt') -Raw).Trim()
$env:PGPASSWORD = $password
$env:RESONANCE_DATABASE_URL = "host=127.0.0.1 port=$port user=resonance dbname=resonance_m1 password=$password sslmode=disable"
Invoke-RestMethod http://127.0.0.1:8080/ready
Invoke-RestMethod 'http://127.0.0.1:8080/api/v1/artists?limit=20'
Invoke-RestMethod http://127.0.0.1:8080/api/v1/library/status
```

The root must be identity-verified and enabled before a scan can reconcile absence. After server startup, library status should show watcher coverage for the enrolled root. Watch events only schedule the authoritative scanner. `/health` is process liveness; `/ready` is the PostgreSQL/schema/grouping check. Keep PostgreSQL loopback-only and do not expose the unauthenticated media server outside a trusted private LAN.

The foreground `-migrate-only` command has a five-minute deadline so a populated grouping backfill can finish at the measured 10k scale. An interrupted migration/backfill remains retryable. A failed command must be inspected and retried before starting the server; readiness rejects incomplete grouping. Normal configured startup retains its ten-second dependency-check deadline.

## Reproducible scale corpus

The separate 10k corpus is deliberately small: 10,000 tagged MP3 files built from four complete MPEG Layer III frames of the CC0 sample, with unique tags and hashes, 100 Artists and 1,000 Albums. It is for catalog and scanner scale, not realistic playback. Generate it in a new directory and preserve `manifest.jsonl` and `summary.json` with the campaign evidence:

```powershell
$scale = Join-Path $work 'scale-10k-seed-20260928'
go run ./cmd/m17corpus -mode scale -tracks 10000 -seed 20260928 -out $scale
```

Do not mix it with the journey corpus. Full-length playback and unbuffered seeking use the 300-second WAV in the journey corpus.

## Stop and retain evidence

Stop the server with Ctrl+C, then stop only the disposable PostgreSQL cluster:

```powershell
pg_ctl -D $cluster -m fast -w stop
if ($LASTEXITCODE -ne 0) { throw "pg_ctl stop failed: $LASTEXITCODE" }
```

Retain each attempt's command, timestamps, exit status, logs, fixture manifest and raw samples. Password files, database dumps, manifests containing private host paths, and personal listening data remain private. See the M1.7 report and complete restore runbook for the wider verification campaign.
