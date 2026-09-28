param(
    [Parameter(Mandatory=$true)][ValidatePattern('^[a-z0-9][a-z0-9-]{0,47}$')][string]$Campaign,
    [Parameter(Mandatory=$true)][string]$Executable,
    [string]$ArgumentsJson = '[]',
    [ValidateRange(1, 21600)][int]$TimeoutSeconds = 1800,
    [string]$WorkingDirectory = (Split-Path -Parent $PSScriptRoot),
    [hashtable]$Environment = @{},
    [string[]]$RemoveEnvironment = @(),
    [switch]$ClearEnvironment,
    [switch]$NoCaptureOutput
)

$ErrorActionPreference = 'Stop'
$parsedArguments = ConvertFrom-Json -InputObject $ArgumentsJson
$argumentTemplate = @($parsedArguments | ForEach-Object { [string]$_ })
$ArgumentList = $argumentTemplate
$repositoryRoot = (Resolve-Path -LiteralPath (Split-Path -Parent $PSScriptRoot)).Path
$campaignRoot = Join-Path $repositoryRoot "docs/benchmarks/m1-7-attempts/$Campaign"
New-Item -ItemType Directory -Force -Path $campaignRoot | Out-Null

$started = [DateTimeOffset]::UtcNow
$stamp = $started.ToString('yyyyMMddTHHmmssfffZ')
$ordinal = 1
do {
    $attemptName = '{0}-attempt-{1:d2}' -f $stamp, $ordinal
    $attemptDirectory = Join-Path $campaignRoot $attemptName
    $ordinal++
} while (Test-Path -LiteralPath $attemptDirectory)
New-Item -ItemType Directory -Path $attemptDirectory | Out-Null
$ArgumentList = @($argumentTemplate | ForEach-Object { $_.Replace('%M17_ATTEMPT_DIR%', $attemptDirectory) })

$stdoutPath = Join-Path $attemptDirectory 'stdout.log'
$stderrPath = Join-Path $attemptDirectory 'stderr.log'
$manifestPath = Join-Path $attemptDirectory 'manifest.json'
$repositoryRoot = (Resolve-Path -LiteralPath $repositoryRoot).Path
$gitArgs = @('-c', 'safe.directory=*', '-C', $repositoryRoot)
$candidateCommit = (& git @gitArgs rev-parse HEAD 2>$null | Select-Object -First 1)
$sourceTreeDirty = [bool](& git @gitArgs status --porcelain=v1 --untracked-files=all 2>$null)
$localPaths = @($repositoryRoot, $env:USERPROFILE, $env:TEMP, $env:TMP) | Where-Object { $_ }
$secretValues = @(
    (Get-ChildItem Env: | Where-Object { $_.Name -match '(?i)(password|passwd|secret|token|database_url|dsn|api.?key)' } | ForEach-Object { $_.Value }),
    ($Environment.GetEnumerator() | Where-Object { $_.Key -match '(?i)(password|passwd|secret|token|database_url|dsn|api.?key)' } | ForEach-Object { [string]$_.Value })
) | Where-Object { $_ }
$redactText = {
    param([string]$Text)
    foreach ($value in $localPaths) {
        # Attempt arguments may themselves contain JSON (for example an
        # ArgumentsJson value passed to a detached process). Redact the path
        # with its common JSON-escaped forms before the plain form so nested
        # command manifests and captured output remain path-free.
        $escapedVariants = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
        $escaped = [string]$value
        for ($depth = 0; $depth -le 4; $depth++) {
            [void]$escapedVariants.Add($escaped)
            [void]$escapedVariants.Add($escaped.Replace('\', '/'))
            $escaped = $escaped.Replace('\', '\\')
        }
        foreach ($variant in $escapedVariants) {
            # Replace the entire path token after a local base, not just its
            # prefix. Nested command JSON may keep the relative suffix after
            # the base directory is hidden.
            $pathPattern = [regex]::Escape($variant) + '(?:(?:\\{1,16}|/)[^\\"/<>|,\]}]+)*'
            $Text = [regex]::Replace($Text, $pathPattern, '<local-path>', [Text.RegularExpressions.RegexOptions]::IgnoreCase)
            $Text = $Text.Replace($variant, '<local-path>', [StringComparison]::OrdinalIgnoreCase)
        }
    }
    foreach ($value in $secretValues) {
        if ($value.Length -ge 4) {
            $escapedVariants = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
            $escaped = [string]$value
            for ($depth = 0; $depth -le 4; $depth++) {
                [void]$escapedVariants.Add($escaped)
                $escaped = $escaped.Replace('\', '\\').Replace('"', '\"')
            }
            foreach ($variant in $escapedVariants) {
                $Text = $Text.Replace($variant, '<redacted-secret>', [StringComparison]::Ordinal)
            }
        }
    }
    $Text = [regex]::Replace($Text, '(?i)(password|passwd|secret|token|api[_-]?key)=([^\s&]+)', '$1=<redacted>')
    return $Text
}

$status = 'launch_failed'
$exitCode = $null
$timedOut = $false
$launchError = $null
$finished = $null
$durationMS = $null
$process = $null
$stdout = ''
$stderr = ''

try {
    $processInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $processInfo.FileName = (Resolve-Path -LiteralPath $Executable).Path
    $processInfo.WorkingDirectory = (Resolve-Path -LiteralPath $WorkingDirectory).Path
    $processInfo.UseShellExecute = $false
    $processInfo.CreateNoWindow = $true
    $processInfo.RedirectStandardOutput = -not $NoCaptureOutput
    $processInfo.RedirectStandardError = -not $NoCaptureOutput
    foreach ($argument in $ArgumentList) { $processInfo.ArgumentList.Add([string]$argument) }
    if ($ClearEnvironment) { $processInfo.Environment.Clear() }
    foreach ($name in $RemoveEnvironment) { [void]$processInfo.Environment.Remove([string]$name) }
    foreach ($name in $Environment.Keys) {
        $value = ([string]$Environment[$name]).Replace('%M17_ATTEMPT_DIR%', $attemptDirectory)
        $processInfo.Environment[[string]$name] = $value
    }
    $processInfo.Environment['RESONANCE_M17_ATTEMPT_DIR'] = $attemptDirectory

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $processInfo
    if (-not $process.Start()) { throw 'Process did not start.' }
    if (-not $NoCaptureOutput) {
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
    }
    if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
        $timedOut = $true
        try { $process.Kill($true) } catch { }
        $process.WaitForExit()
    }
    if (-not $NoCaptureOutput) {
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
    }
    $finished = [DateTimeOffset]::UtcNow
    $durationMS = [Math]::Round(($finished - $started).TotalMilliseconds, 3)
    if ($timedOut) {
        $status = 'timeout'
    } else {
        $exitCode = $process.ExitCode
        $status = if ($exitCode -eq 0) { 'pass' } else { 'fail' }
    }
} catch {
    $finished = [DateTimeOffset]::UtcNow
    $durationMS = [Math]::Round(($finished - $started).TotalMilliseconds, 3)
    $launchError = (& $redactText $_.Exception.Message)
    $stderr = $launchError
}

[IO.File]::WriteAllText($stdoutPath, (& $redactText $stdout), [Text.UTF8Encoding]::new($false))
[IO.File]::WriteAllText($stderrPath, (& $redactText $stderr), [Text.UTF8Encoding]::new($false))
$safeCommand = @((Split-Path -Leaf $Executable)) + @($argumentTemplate | ForEach-Object { & $redactText ([string]$_) })
$manifest = [ordered]@{
    attempt_id = $attemptName
    campaign = $Campaign
    status = $status
    exit_code = $exitCode
    timed_out = $timedOut
    started_utc = $started.ToString('o')
    finished_utc = $finished.ToString('o')
    duration_ms = $durationMS
    candidate_commit = $candidateCommit
    source_tree_dirty = $sourceTreeDirty
    command = $safeCommand
    working_directory = (& $redactText ([string]$WorkingDirectory))
    timeout_seconds = $TimeoutSeconds
    child_environment_cleared = [bool]$ClearEnvironment
    output_capture = if ($NoCaptureOutput) { 'disabled; child manages its own output files' } else { 'captured' }
    removed_environment_variable_names = @($RemoveEnvironment | Sort-Object)
    provided_environment_variable_names = @($Environment.Keys | ForEach-Object { [string]$_ } | Sort-Object)
    output_redactions = @('repository root', 'user profile', 'temporary directory', 'secret values')
    launch_error = $launchError
}
$manifest | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath -Encoding utf8
Write-Output (Join-Path "docs/benchmarks/m1-7-attempts/$Campaign" $attemptName)
if ($status -ne 'pass') { exit 1 }
