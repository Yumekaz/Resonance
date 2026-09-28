param(
    [Parameter(Mandatory=$true)][string]$GoExe,
    [Parameter(Mandatory=$true)][string]$SourceDirectory,
    [Parameter(Mandatory=$true)][string]$RootId,
    [ValidateRange(1,500)][int]$Repetitions = 30
)

$ErrorActionPreference = 'Stop'
$attemptDirectory = $env:RESONANCE_M17_ATTEMPT_DIR
if (-not $attemptDirectory) { throw 'Run this benchmark through m1-7-attempt.ps1.' }
$samplesPath = Join-Path $attemptDirectory 'scan-samples.jsonl'
$summaryPath = Join-Path $attemptDirectory 'scan-summary.json'
$writer = [IO.StreamWriter]::new([IO.FileStream]::new($samplesPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read), [Text.UTF8Encoding]::new($false))
$durations = [Collections.Generic.List[double]]::new()
try {
    for ($i = 1; $i -le $Repetitions; $i++) {
        $started = [DateTimeOffset]::UtcNow
        $raw = @(& $GoExe run . library scan $RootId 2>&1)
        $exitCode = $LASTEXITCODE
        $finished = [DateTimeOffset]::UtcNow
        $text = ($raw | ForEach-Object { [string]$_ }) -join "`n"
        if ($exitCode -ne 0) {
            $diagnostic = "exit_code=$exitCode`nexecutable_exists=$(Test-Path -LiteralPath $GoExe)`nworking_directory=$((Get-Location).Path)`n" + $text
            [IO.File]::WriteAllText((Join-Path $attemptDirectory "scan-$i-error.log"), $diagnostic + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
            throw "unchanged scan $i exited $exitCode"
        }
        $scan = $text | ConvertFrom-Json
        if ($scan.status -ne 'succeeded' -or $scan.files_hashed -ne 0 -or $scan.metadata_extractions -ne 0 -or -not $scan.absence_reconciled) {
            throw "unchanged scan $i did not remain an authoritative zero-hash scan"
        }
        $duration = [double]$scan.duration_ms
        $durations.Add($duration)
        $sample = [ordered]@{
            index = $i
            started_utc = $started.ToString('o')
            finished_utc = $finished.ToString('o')
            process_duration_ms = [Math]::Round(($finished - $started).TotalMilliseconds, 3)
            scan_duration_ms = $duration
            files_visited = $scan.files_visited
            files_supported = $scan.files_supported
            files_hashed = $scan.files_hashed
            metadata_extractions = $scan.metadata_extractions
            grouping_ms = $scan.grouping_ms
            publish_transaction_ms = $scan.publish_transaction_ms
            sql_statements = $scan.sql_statements
            rows_affected = $scan.rows_affected
            traversal_complete = $scan.traversal_complete
            absence_reconciled = $scan.absence_reconciled
            status = $scan.status
        }
        $writer.WriteLine(($sample | ConvertTo-Json -Depth 6 -Compress))
        $writer.Flush()
    }
} finally {
    $writer.Dispose()
}
$sorted = @($durations | Sort-Object)
$middle = [int][Math]::Floor($sorted.Count / 2)
$p50 = if ($sorted.Count % 2 -eq 0) { ($sorted[$middle - 1] + $sorted[$middle]) / 2 } else { $sorted[$middle] }
$nearest = { param([double]$p) $rank=[Math]::Max(1,[int][Math]::Ceiling($sorted.Count*$p)); return $sorted[$rank-1] }
$summary = [ordered]@{
    dataset = '10,000 small tagged MP3 fixtures; 100 Artists; 1,000 Albums'
    repetition_count = $Repetitions
    operation = 'complete unchanged authoritative scan'
    cache_state = 'warm/uncontrolled; no OS cache flush'
    percentile_method = 'median p50; nearest-rank p95/p99'
    p50_ms = [Math]::Round($p50, 3)
    p95_ms = [Math]::Round((& $nearest 0.95), 3)
    p99_ms = [Math]::Round((& $nearest 0.99), 3)
    hash_count_per_sample = 0
    metadata_extractions_per_sample = 0
    raw_samples = 'scan-samples.jsonl'
}
$summary | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $summaryPath -Encoding utf8
Write-Output ($summary | ConvertTo-Json -Depth 6 -Compress)
