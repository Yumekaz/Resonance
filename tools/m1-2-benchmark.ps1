param(
    [Parameter(Mandatory)][string]$RootId,
    [string]$ServerUrl = 'http://127.0.0.1:8080/media/demo-track',
    [string]$ServerExe = 'data/m12-server.exe',
    [string]$BenchExe = 'data/m12-bench.exe',
    [string]$PgBin = 'data/postgresql-17.11/pgsql/bin',
    [string]$OutputPrefix = 'docs/benchmarks/M1-2'
)

$ErrorActionPreference = 'Stop'
if (-not $env:RESONANCE_DATABASE_URL) { throw 'Set RESONANCE_DATABASE_URL in this terminal.' }
if (-not $env:PGPASSWORD) { throw 'Set PGPASSWORD in this terminal for the size query.' }
$serverPath = (Resolve-Path -LiteralPath $ServerExe).Path
$benchPath = (Resolve-Path -LiteralPath $BenchExe).Path
$pgPath = (Resolve-Path -LiteralPath $PgBin).Path
$prefix = [System.IO.Path]::GetFullPath((Join-Path (Get-Location) $OutputPrefix))

& $benchPath -url $ServerUrl -count 100 -out "$prefix-idle-playback.json" | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Idle playback benchmark failed.' }

$scanStdout = "$prefix-scan-result.jsonl"
$scanStderr = "$prefix-scan-log.jsonl"
$benchStdout = "$prefix-concurrent-bench.log"
$benchStderr = "$prefix-concurrent-bench-error.log"
$started = [DateTimeOffset]::UtcNow
$scan = Start-Process -FilePath $serverPath -ArgumentList @('library','scan',$RootId) -WindowStyle Hidden -PassThru -RedirectStandardOutput $scanStdout -RedirectStandardError $scanStderr
$deadline = [DateTimeOffset]::UtcNow.AddSeconds(10)
$scanObserved = $false
while ([DateTimeOffset]::UtcNow -lt $deadline) {
    if ((Test-Path -LiteralPath $scanStderr) -and (Get-Content -LiteralPath $scanStderr -Raw -ErrorAction SilentlyContinue) -match 'scan_started') { $scanObserved = $true; break }
    $scan.Refresh()
    if ($scan.HasExited) { throw "Scan exited before playback benchmark (code $($scan.ExitCode))." }
    Start-Sleep -Milliseconds 20
}
if (-not $scanObserved) { throw 'Scan start was not observed before the playback benchmark deadline.' }
$benchStart = [DateTimeOffset]::UtcNow
$bench = Start-Process -FilePath $benchPath -ArgumentList @('-url',$ServerUrl,'-count','100','-out',"$prefix-concurrent-playback.json") -WindowStyle Hidden -PassThru -RedirectStandardOutput $benchStdout -RedirectStandardError $benchStderr
$peakBytes = [int64]0
$cpuSeconds = 0.0
$scanEnd = $null
$benchEnd = $null
while ($true) {
    $scan.Refresh(); $bench.Refresh()
    if (-not $scan.HasExited) {
        try {
            $peakBytes = [Math]::Max($peakBytes, [int64]$scan.PeakWorkingSet64)
            $cpuSeconds = [Math]::Max($cpuSeconds, $scan.TotalProcessorTime.TotalSeconds)
        } catch {}
    } elseif (-not $scanEnd) { $scanEnd = [DateTimeOffset]::UtcNow }
    if ($bench.HasExited -and -not $benchEnd) { $benchEnd = [DateTimeOffset]::UtcNow }
    if ($scanEnd -and $benchEnd) { break }
    Start-Sleep -Milliseconds 50
}
if ($scan.ExitCode -ne 0) { throw "Scan failed (code $($scan.ExitCode)); inspect $scanStderr." }
if ($bench.ExitCode -ne 0) { throw "Concurrent playback benchmark failed (code $($bench.ExitCode))." }
$scanResult = (Get-Content -LiteralPath $scanStdout | Select-Object -Last 1 | ConvertFrom-Json)
$scanEvents = @(Get-Content -LiteralPath $scanStderr | ForEach-Object { $_ | ConvertFrom-Json })
$scanStartedEvent = $scanEvents | Where-Object msg -eq 'scan_started' | Select-Object -First 1
$scanFinishedEvent = $scanEvents | Where-Object msg -eq 'scan_finished' | Select-Object -Last 1
if (-not $scanStartedEvent -or -not $scanFinishedEvent) { throw 'Scan boundary telemetry is incomplete.' }
$scanActualStart = [DateTimeOffset]::Parse($scanStartedEvent.time)
$scanActualEnd = [DateTimeOffset]::Parse($scanFinishedEvent.time)
$idle = Get-Content -LiteralPath "$prefix-idle-playback.json" -Raw | ConvertFrom-Json
$concurrent = Get-Content -LiteralPath "$prefix-concurrent-playback.json" -Raw | ConvertFrom-Json
$dbSize = & (Join-Path $pgPath 'psql.exe') -h 127.0.0.1 -p 55432 -U resonance -d resonance_m1 -tAc 'SELECT pg_database_size(current_database())'
if ($LASTEXITCODE -ne 0) { throw 'Database size query failed.' }
$duration = [Math]::Max(0.001, [double]$scanResult.duration_ms / 1000)
$overlap = [Math]::Max(0, ([Math]::Min($scanActualEnd.ToUnixTimeMilliseconds(), $benchEnd.ToUnixTimeMilliseconds()) - [Math]::Max($scanActualStart.ToUnixTimeMilliseconds(), $benchStart.ToUnixTimeMilliseconds())))
$report = [pscustomobject]@{
    timestamp_utc = $started.ToString('o')
    root_id = $RootId
    scan_run_id = $scanResult.run_id
    scan_status = $scanResult.status
    scan_duration_ms = $scanResult.duration_ms
    files_visited = $scanResult.files_visited
    files_supported = $scanResult.files_supported
    imported = $scanResult.imported
    skipped = $scanResult.skipped
    failed = $scanResult.failed
    metadata_extractions = $scanResult.metadata_extractions
    bytes_hashed = $scanResult.bytes_hashed
    supported_files_per_second = [Math]::Round($scanResult.files_supported / $duration, 2)
    hashed_bytes_per_second = [Math]::Round($scanResult.bytes_hashed / $duration, 2)
    scanner_peak_working_set_bytes = $peakBytes
    scanner_sampled_cpu_seconds = [Math]::Round($cpuSeconds, 3)
    postgres_database_bytes_after = [int64]$dbSize.Trim()
    playback_overlap_ms = $overlap
    scan_started_utc = $scanActualStart.ToUniversalTime().ToString('o')
    scan_finished_utc = $scanActualEnd.ToUniversalTime().ToString('o')
    concurrent_benchmark_started_utc = $benchStart.ToUniversalTime().ToString('o')
    concurrent_benchmark_finished_utc = $benchEnd.ToUniversalTime().ToString('o')
    idle_range_median_ms = $idle.random_range_total_median_ms
    idle_range_p95_ms = $idle.random_range_total_p95_ms
    concurrent_range_median_ms = $concurrent.random_range_total_median_ms
    concurrent_range_p95_ms = $concurrent.random_range_total_p95_ms
}
$report | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath "$prefix-summary.json"
$report | ConvertTo-Json -Depth 4
