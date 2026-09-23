param(
    [ValidateRange(3, 10)][int]$Repetitions = 10,
    [switch]$ReplaceEvidence,
    [string]$GoExe = 'go',
    [string]$PgBin = 'data/postgresql-17.11/pgsql/bin',
    [string]$RangeUrl = 'http://127.0.0.1:18080/media/demo-track'
)

$ErrorActionPreference = 'Stop'
 $originalDatabaseUrl = $env:RESONANCE_DATABASE_URL
 $originalBenchmarkOutput = $env:RESONANCE_BENCHMARK_OUTPUT
 $originalBenchmarkLog = $env:RESONANCE_BENCHMARK_LOG
 $originalBenchmarkRepetitions = $env:RESONANCE_BENCHMARK_REPETITIONS
 $originalBenchmarkRangeUrl = $env:RESONANCE_BENCHMARK_RANGE_URL
 $originalBenchmarkRangeBench = $env:RESONANCE_BENCHMARK_RANGE_BENCH
 $originalBenchmarkRangeOutput = $env:RESONANCE_BENCHMARK_RANGE_OUTPUT
if (-not $env:RESONANCE_TEST_DATABASE_URL) { throw 'Set RESONANCE_TEST_DATABASE_URL to a dedicated empty test database.' }
if (-not $env:PGPASSWORD) { throw 'Set PGPASSWORD in this terminal. The script never reads or prints a password.' }
if ($env:RESONANCE_TEST_DATABASE_URL -match '^(postgres|postgresql)://') {
    throw 'Use a keyword-style PostgreSQL connection string so the temporary benchmark schema can be added safely.'
}

$pgPath = (Resolve-Path -LiteralPath $PgBin).Path
$repositoryRoot = (Resolve-Path -LiteralPath '.').Path
$serverExe = Join-Path $repositoryRoot 'data/m13a-server.exe'
$benchExe = Join-Path $repositoryRoot 'data/m13a-bench.exe'
$testExe = Join-Path $repositoryRoot 'data/m13a-benchmark.test.exe'
$outputDir = Join-Path $repositoryRoot 'docs/benchmarks'
$outputs = @(
    'M1-3A-range-idle.json',
    'M1-3A-range-concurrent.json',
    'M1-3A-raw.json',
    'M1-3A-scan-events.jsonl',
    'M1-3A-summary.json'
) | ForEach-Object { Join-Path $outputDir $_ }
foreach ($path in $outputs) {
    if ((Test-Path -LiteralPath $path) -and -not $ReplaceEvidence) { throw "Benchmark output already exists; preserve it and choose a fresh copy of the report names: $path" }
}

$schema = 'm13a_http_' + [guid]::NewGuid().ToString('N')
$databaseUrl = $env:RESONANCE_TEST_DATABASE_URL
$serverDatabaseUrl = "$databaseUrl search_path=$schema"
$psql = Join-Path $pgPath 'psql.exe'
$schemaCreated = $false
$server = $null
$test = $null
$testOutput = Join-Path $repositoryRoot 'data/m13a-benchmark-test.stdout.log'
$testError = Join-Path $repositoryRoot 'data/m13a-benchmark-test.stderr.log'
$serverOutput = Join-Path $repositoryRoot 'data/m13a-benchmark-server.stdout.log'
$serverError = Join-Path $repositoryRoot 'data/m13a-benchmark-server.stderr.log'
$testStarted = [DateTimeOffset]::UtcNow
$peakWorkingSet = [int64]0
$peakCPUSeconds = 0.0

try {
    New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
    & $psql -d $databaseUrl -X -v ON_ERROR_STOP=1 -c "CREATE SCHEMA $schema" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not create isolated benchmark schema.' }
    $schemaCreated = $true

    $env:RESONANCE_DATABASE_URL = $serverDatabaseUrl
    & $GoExe run . -migrate-only
    if ($LASTEXITCODE -ne 0) { throw 'Migrations failed in the isolated HTTP benchmark schema.' }
    & $GoExe build -o $serverExe .
    if ($LASTEXITCODE -ne 0) { throw 'Server build failed.' }
    & $GoExe build -o $benchExe ./cmd/bench
    if ($LASTEXITCODE -ne 0) { throw 'Range benchmark build failed.' }
    & $GoExe test -p 1 '-tags=integration,benchmark' -c -o $testExe ./internal/library
    if ($LASTEXITCODE -ne 0) { throw 'Benchmark test build failed.' }

    $server = Start-Process -FilePath $serverExe -ArgumentList @('-addr', '127.0.0.1:18080', '-media', 'data/demo.wav') -WindowStyle Hidden -PassThru -RedirectStandardOutput $serverOutput -RedirectStandardError $serverError
    $healthUrl = 'http://127.0.0.1:18080/health'
    $readyDeadline = [DateTimeOffset]::UtcNow.AddSeconds(20)
    $ready = $false
    while ([DateTimeOffset]::UtcNow -lt $readyDeadline) {
        try {
            $response = Invoke-WebRequest -Uri $healthUrl -TimeoutSec 2
            if ($response.StatusCode -eq 200) { $ready = $true; break }
        } catch {}
        $server.Refresh()
        if ($server.HasExited) { throw "HTTP server exited before readiness (code $($server.ExitCode))." }
        Start-Sleep -Milliseconds 200
    }
    if (-not $ready) { throw 'HTTP server did not become ready.' }

    & $benchExe -url $RangeUrl -count 100 -out $outputs[0] | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Idle Range benchmark failed.' }

    $env:RESONANCE_TEST_DATABASE_URL = $databaseUrl
    $env:RESONANCE_BENCHMARK_OUTPUT = $outputs[2]
    $env:RESONANCE_BENCHMARK_LOG = $outputs[3]
    $env:RESONANCE_BENCHMARK_REPETITIONS = [string]$Repetitions
    $env:RESONANCE_BENCHMARK_RANGE_URL = $RangeUrl
    $env:RESONANCE_BENCHMARK_RANGE_BENCH = $benchExe
    $env:RESONANCE_BENCHMARK_RANGE_OUTPUT = $outputs[1]
    $testStarted = [DateTimeOffset]::UtcNow
    $test = Start-Process -FilePath $testExe -WorkingDirectory (Join-Path $repositoryRoot 'internal/library') -ArgumentList @('-test.run=^TestM13BenchmarkSuite$', '-test.count=1', '-test.v') -WindowStyle Hidden -PassThru -RedirectStandardOutput $testOutput -RedirectStandardError $testError
    while ($true) {
        $test.Refresh()
        if ($test.HasExited) { break }
        try {
            $peakWorkingSet = [Math]::Max($peakWorkingSet, [int64]$test.PeakWorkingSet64)
            $peakCPUSeconds = [Math]::Max($peakCPUSeconds, $test.TotalProcessorTime.TotalSeconds)
        } catch {}
        Start-Sleep -Milliseconds 50
    }
    if ($test.ExitCode -ne 0) { throw "Benchmark suite failed (code $($test.ExitCode)); inspect the local benchmark test logs." }
    $test.Refresh()
    $peakWorkingSet = [Math]::Max($peakWorkingSet, [int64]$test.PeakWorkingSet64)
    $peakCPUSeconds = [Math]::Max($peakCPUSeconds, $test.TotalProcessorTime.TotalSeconds)

    $raw = Get-Content -LiteralPath $outputs[2] -Raw | ConvertFrom-Json
    $idle = Get-Content -LiteralPath $outputs[0] -Raw | ConvertFrom-Json
    $concurrent = Get-Content -LiteralPath $outputs[1] -Raw | ConvertFrom-Json
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class ResonanceMemoryStatus {
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Auto)]
    public struct MEMORYSTATUSEX {
        public uint dwLength; public uint dwMemoryLoad;
        public ulong ullTotalPhys; public ulong ullAvailPhys;
        public ulong ullTotalPageFile; public ulong ullAvailPageFile;
        public ulong ullTotalVirtual; public ulong ullAvailVirtual;
        public ulong ullAvailExtendedVirtual;
    }
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool GlobalMemoryStatusEx(ref MEMORYSTATUSEX status);
    public static ulong TotalPhysicalBytes() {
        var status = new MEMORYSTATUSEX();
        status.dwLength = (uint)Marshal.SizeOf(typeof(MEMORYSTATUSEX));
        if (!GlobalMemoryStatusEx(ref status)) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
        return status.ullTotalPhys;
    }
}
'@
    $driveRoot = [System.IO.Path]::GetPathRoot($repositoryRoot)
    $driveDevice = $driveRoot.TrimEnd('\')
    $drive = [System.IO.DriveInfo]::new($driveRoot)
    $summary = [pscustomobject]@{
        timestamp_utc = $testStarted.ToString('o')
        command = "pwsh -NoProfile -File tools/m1-3a-benchmark.ps1 -Repetitions $Repetitions -GoExe '$GoExe'$(if ($ReplaceEvidence) { ' -ReplaceEvidence' })"
        machine = [pscustomobject]@{
            os = [System.Runtime.InteropServices.RuntimeInformation]::OSDescription
            logical_cpus = [Environment]::ProcessorCount
            physical_memory_bytes = [int64][ResonanceMemoryStatus]::TotalPhysicalBytes()
            repository_drive = $driveDevice
            drive_type = [string]$drive.DriveType
            drive_file_system = $drive.DriveFormat
            drive_size_bytes = [int64]$drive.TotalSize
            drive_free_bytes = [int64]$drive.AvailableFreeSpace
            physical_disk_model = 'not available in the restricted PowerShell session'
        }
        test_database = 'dedicated test database; isolated migration schema dropped after this run'
        repetitions = $Repetitions
        test_process_peak_working_set_bytes = $peakWorkingSet
        test_process_sampled_cpu_seconds = [Math]::Round($peakCPUSeconds, 3)
        benchmark_suite_duration_ms = [Math]::Round(([DateTimeOffset]::UtcNow - $testStarted).TotalMilliseconds, 3)
        initial_database_size_bytes = $raw.database_size_bytes_before
        final_database_size_bytes = $raw.database_size_bytes_after
        wal_bytes_generated = $raw.wal_bytes_generated
        idle_range_p50_ms = $idle.random_range_total_median_ms
        idle_range_p95_ms = $idle.random_range_total_p95_ms
        idle_range_p99_ms = $idle.random_range_total_p99_ms
        concurrent_range_samples_overlapping = $raw.range_overlap.overlapping_requests
        concurrent_range_p50_ms = $raw.range_overlap.overlapping_range_p50_ms
        concurrent_range_p95_ms = $raw.range_overlap.overlapping_range_p95_ms
        concurrent_range_p99_ms = $raw.range_overlap.overlapping_range_p99_ms
        concurrent_range_max_ms = $raw.range_overlap.overlapping_range_max_ms
        range_overlap_ms = $raw.range_overlap.overlap_ms
        scan_workloads = $raw.workload_percentiles
        cold_cache_note = $raw.cache_conditions.first_pass
    }
    $summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $outputs[4]
    $summary | ConvertTo-Json -Depth 8
}
finally {
    if ($test -and -not $test.HasExited) {
        Stop-Process -Id $test.Id -Force -ErrorAction SilentlyContinue
    }
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
    if ($schemaCreated) {
        & $psql -d $databaseUrl -X -v ON_ERROR_STOP=1 -c "DROP SCHEMA $schema CASCADE" | Out-Null
    }
    if (Test-Path -LiteralPath $testExe) { Remove-Item -LiteralPath $testExe -Force }
    if ($null -eq $originalDatabaseUrl) { Remove-Item Env:RESONANCE_DATABASE_URL -ErrorAction SilentlyContinue } else { $env:RESONANCE_DATABASE_URL = $originalDatabaseUrl }
    if ($null -eq $originalBenchmarkOutput) { Remove-Item Env:RESONANCE_BENCHMARK_OUTPUT -ErrorAction SilentlyContinue } else { $env:RESONANCE_BENCHMARK_OUTPUT = $originalBenchmarkOutput }
    if ($null -eq $originalBenchmarkLog) { Remove-Item Env:RESONANCE_BENCHMARK_LOG -ErrorAction SilentlyContinue } else { $env:RESONANCE_BENCHMARK_LOG = $originalBenchmarkLog }
    if ($null -eq $originalBenchmarkRepetitions) { Remove-Item Env:RESONANCE_BENCHMARK_REPETITIONS -ErrorAction SilentlyContinue } else { $env:RESONANCE_BENCHMARK_REPETITIONS = $originalBenchmarkRepetitions }
    if ($null -eq $originalBenchmarkRangeUrl) { Remove-Item Env:RESONANCE_BENCHMARK_RANGE_URL -ErrorAction SilentlyContinue } else { $env:RESONANCE_BENCHMARK_RANGE_URL = $originalBenchmarkRangeUrl }
    if ($null -eq $originalBenchmarkRangeBench) { Remove-Item Env:RESONANCE_BENCHMARK_RANGE_BENCH -ErrorAction SilentlyContinue } else { $env:RESONANCE_BENCHMARK_RANGE_BENCH = $originalBenchmarkRangeBench }
    if ($null -eq $originalBenchmarkRangeOutput) { Remove-Item Env:RESONANCE_BENCHMARK_RANGE_OUTPUT -ErrorAction SilentlyContinue } else { $env:RESONANCE_BENCHMARK_RANGE_OUTPUT = $originalBenchmarkRangeOutput }
}
