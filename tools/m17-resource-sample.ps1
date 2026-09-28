param(
    [Parameter(Mandatory=$true)][string]$ProcessIdsCsv,
    [string]$BaseUrl = 'http://127.0.0.1:18084',
    [string]$PostgresExePath = '',
    [string]$PsqlPath = '',
    [string]$DatabaseName = '',
    [string]$Port = '55439',
    [ValidateRange(10,3600)][int]$DurationSeconds = 30,
    [ValidateRange(250,10000)][int]$IntervalMilliseconds = 1000
)

$ErrorActionPreference = 'Stop'
$attemptDirectory = $env:RESONANCE_M17_ATTEMPT_DIR
if (-not $attemptDirectory) { throw 'Run this sampler through m1-7-attempt.ps1.' }
function Get-DatabaseCounters([string]$WalStart = '') {
    if (-not $WalStart) {
        $query = "SELECT pg_database_size(current_database())::text || '|' || pg_current_wal_lsn()::text"
    } else {
        $query = "SELECT pg_database_size(current_database())::text || '|' || pg_wal_lsn_diff(pg_current_wal_lsn(),'$WalStart'::pg_lsn)::bigint::text || '|' || pg_current_wal_lsn()::text"
    }
    $raw = & $PsqlPath -X -A -t -v ON_ERROR_STOP=1 -h 127.0.0.1 -p $Port -U resonance -d $DatabaseName -c $query
    if ($LASTEXITCODE -ne 0) { throw 'Could not read PostgreSQL size and WAL counters.' }
    $fields = ([string]($raw | Select-Object -Last 1)).Split('|')
    if (-not $WalStart) { return [pscustomobject]@{size_bytes=[int64]$fields[0];wal_lsn=$fields[1]} }
    return [pscustomobject]@{size_bytes=[int64]$fields[0];wal_bytes=[int64]$fields[1];wal_lsn=$fields[2]}
}
$ids = @($ProcessIdsCsv.Split(',') | ForEach-Object { [int]$_.Trim() })
$rawPath = Join-Path $attemptDirectory 'resource-samples.jsonl'
$summaryPath = Join-Path $attemptDirectory 'resource-summary.json'
$raw = [IO.StreamWriter]::new([IO.FileStream]::new($rawPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read), [Text.UTF8Encoding]::new($false))
$samples = [Collections.Generic.List[object]]::new()
$prior = @{}
$started = [DateTimeOffset]::UtcNow
$statusStart = Invoke-RestMethod -Uri "$BaseUrl/api/v1/library/status" -TimeoutSec 5
$databaseStart = $null
if ($PsqlPath -and $DatabaseName) { $databaseStart = Get-DatabaseCounters }
$deadline = [Diagnostics.Stopwatch]::StartNew()
function Get-Median([double[]]$values) {
    if ($values.Count % 2 -eq 0) {
        $middle = [int]($values.Count / 2)
        return ($values[$middle - 1] + $values[$middle]) / 2
    }
    return $values[[int][Math]::Floor($values.Count/2)]
}
try {
    while ($deadline.Elapsed.TotalSeconds -lt $DurationSeconds) {
        $at = [DateTimeOffset]::UtcNow
        $processStates = @()
        foreach ($processId in $ids) {
            $process = Get-Process -Id $processId -ErrorAction Stop
            $cpuMs = $process.TotalProcessorTime.TotalMilliseconds
            $cpuPercent = $null
            if ($prior.ContainsKey($processId)) {
                $elapsedMs = ($at - $prior[$processId].time).TotalMilliseconds
                if ($elapsedMs -gt 0) { $cpuPercent = 100 * ($cpuMs - $prior[$processId].cpu_ms) / $elapsedMs / [Environment]::ProcessorCount }
            }
            $prior[$processId] = @{time=$at;cpu_ms=$cpuMs}
            $processStates += [ordered]@{
                role = if ($process.ProcessName -eq 'resonance-final') { 'resonance' } else { 'postgresql' }
                pid = $process.Id
                cpu_total_ms = [Math]::Round($cpuMs, 3)
                cpu_percent_since_previous_sample = if ($null -eq $cpuPercent) { $null } else { [Math]::Round($cpuPercent, 4) }
                working_set_bytes = [int64]$process.WorkingSet64
                handles = [int]$process.HandleCount
                threads = [int]$process.Threads.Count
            }
        }
        if ($PostgresExePath) {
            $postgresProcesses = @(Get-Process postgres -ErrorAction SilentlyContinue | Where-Object { $_.Path -ieq $PostgresExePath })
            if ($postgresProcesses.Count -gt 0) {
                $cpuMs = 0.0; $workingSet = [int64]0; $handles = 0; $threads = 0; $pids = @()
                foreach ($process in $postgresProcesses) {
                    $cpuMs += $process.TotalProcessorTime.TotalMilliseconds
                    $workingSet += [int64]$process.WorkingSet64
                    $handles += [int]$process.HandleCount
                    $threads += [int]$process.Threads.Count
                    $pids += $process.Id
                }
                $cpuPercent = $null
                if ($prior.ContainsKey('postgresql')) {
                    $elapsedMs = ($at - $prior['postgresql'].time).TotalMilliseconds
                    if ($elapsedMs -gt 0) { $cpuPercent = 100 * ($cpuMs - $prior['postgresql'].cpu_ms) / $elapsedMs / [Environment]::ProcessorCount }
                }
                $prior['postgresql'] = @{time=$at;cpu_ms=$cpuMs}
                $processStates += [ordered]@{
                    role='postgresql'; process_count=$postgresProcesses.Count; pids=$pids
                    cpu_total_ms=[Math]::Round($cpuMs,3)
                    cpu_percent_since_previous_sample=if($null -eq $cpuPercent){$null}else{[Math]::Round($cpuPercent,4)}
                    working_set_bytes=$workingSet; handles=$handles; threads=$threads
                }
            }
        }
        $sample = [ordered]@{
            sampled_utc = $at.ToString('o')
            elapsed_seconds = [Math]::Round($deadline.Elapsed.TotalSeconds, 3)
            processes = $processStates
        }
        $encoded = $sample | ConvertTo-Json -Depth 6 -Compress
        $raw.WriteLine($encoded)
        $raw.Flush()
        $samples.Add($sample)
        Start-Sleep -Milliseconds $IntervalMilliseconds
    }
} finally {
    $raw.Dispose()
}

$statusEnd = Invoke-RestMethod -Uri "$BaseUrl/api/v1/library/status" -TimeoutSec 5
$databaseEnd = $null
if ($databaseStart) { $databaseEnd = Get-DatabaseCounters $databaseStart.wal_lsn }
$summaryProcesses = @()
foreach ($role in @('resonance','postgresql')) {
    $rows = @($samples | ForEach-Object { $_.processes } | Where-Object { $_.role -eq $role })
    if ($rows.Count -eq 0) { continue }
    $working = @($rows | ForEach-Object { [double]$_.working_set_bytes / 1MB } | Sort-Object)
    $handles = @($rows | ForEach-Object { [double]$_.handles } | Sort-Object)
    $cpu = @($rows | ForEach-Object { $_.cpu_percent_since_previous_sample } | Where-Object { $null -ne $_ } | ForEach-Object { [double]$_ } | Sort-Object)
    $summaryProcesses += [ordered]@{
        role=$role
        process_samples=$rows.Count
        working_set_mib_p50=[Math]::Round((Get-Median $working),3)
        working_set_mib_p95=[Math]::Round($working[[Math]::Ceiling($working.Count*.95)-1],3)
        working_set_mib_p99=[Math]::Round($working[[Math]::Ceiling($working.Count*.99)-1],3)
        handles_p50=[Math]::Round((Get-Median $handles),0)
        handles_max=($handles|Measure-Object -Maximum).Maximum
        cpu_percent_p50=if($cpu.Count){[Math]::Round((Get-Median $cpu),3)}else{$null}
        cpu_percent_p95=if($cpu.Count){[Math]::Round($cpu[[Math]::Ceiling($cpu.Count*.95)-1],3)}else{$null}
        cpu_percent_max=if($cpu.Count){($cpu|Measure-Object -Maximum).Maximum}else{$null}
    }
}
$report = [ordered]@{
    started_utc=$started.ToString('o')
    finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
    duration_seconds=$DurationSeconds
    sample_interval_ms=$IntervalMilliseconds
    sample_count=$samples.Count
    database_size_bytes_start=if($databaseStart){$databaseStart.size_bytes}else{$null}
    database_size_bytes_end=if($databaseEnd){$databaseEnd.size_bytes}else{$null}
    wal_bytes_generated=if($databaseEnd){$databaseEnd.wal_bytes}else{$null}
    logical_cpu_count=[Environment]::ProcessorCount
    workload=$env:RESONANCE_M17_RESOURCE_WORKLOAD
    watcher_observations=@{
        start_state=$statusStart.watcher_state;start_database_state=$statusStart.database_state;start_roots_total=$statusStart.roots_total
        start_roots_watching=@($statusStart.roots | Where-Object { $_.watch_state -eq 'watching' }).Count
        start_dirty_roots=$statusStart.dirty_roots
        end_state=$statusEnd.watcher_state;end_database_state=$statusEnd.database_state;end_roots_total=$statusEnd.roots_total
        end_roots_watching=@($statusEnd.roots | Where-Object { $_.watch_state -eq 'watching' }).Count
        end_dirty_roots=$statusEnd.dirty_roots
    }
    processes=$summaryProcesses
    raw_samples='resource-samples.jsonl'
    limitations='Per-process working set, handles and CPU are sampled locally; the API exposes watch coverage state but not native watcher-handle count.'
}
$report | ConvertTo-Json -Depth 7 | Set-Content -LiteralPath $summaryPath -Encoding utf8
Write-Output ($report | ConvertTo-Json -Depth 5 -Compress)
