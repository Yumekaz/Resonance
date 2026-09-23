param(
    [Parameter(Mandatory=$true)][string]$RootID,
    [string]$ServerExe = 'data/m14-server.exe',
    [ValidateRange(3, 30)][int]$WarmScans = 10,
    [string]$Output = 'docs/benchmarks/M1-4-publication-new.json'
)

$ErrorActionPreference = 'Stop'
if (-not $env:RESONANCE_DATABASE_URL) { throw 'Set RESONANCE_DATABASE_URL to a dedicated migrated test schema.' }
if (Test-Path -LiteralPath $Output) { throw "Output exists; choose a fresh file: $Output" }
$exe = (Resolve-Path -LiteralPath $ServerExe).Path
$runs = @()
for ($i = 0; $i -le $WarmScans; $i++) {
    $started = [DateTimeOffset]::UtcNow.ToString('o')
    $result = & $exe library scan $RootID 2>$null | ConvertFrom-Json
    $finished = [DateTimeOffset]::UtcNow.ToString('o')
    if ($LASTEXITCODE -ne 0 -or $result.status -ne 'succeeded') { throw "Scan $i did not succeed" }
    $runs += @{ iteration = $i; started_utc = $started; finished_utc = $finished; scan = $result }
}
@{
    date_utc = [DateTimeOffset]::UtcNow.ToString('o')
    command = 'powershell -File tools/m1-4-publication-benchmark.ps1 -RootID <id> -ServerExe data/m14-server.exe -WarmScans 10 -Output <fresh-output>'
    root_id = $RootID
    runs = $runs
} | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $Output -Encoding utf8
Write-Output "Wrote $($runs.Count) raw scans to $Output"
