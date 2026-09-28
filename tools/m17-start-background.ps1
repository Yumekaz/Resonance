param(
    [Parameter(Mandatory=$true)][string]$Executable,
    [string]$ArgumentsJson = '[]',
    [string]$WorkingDirectory = (Get-Location).Path
)

$ErrorActionPreference = 'Stop'
$attemptDirectory = $env:RESONANCE_M17_ATTEMPT_DIR
if (-not $attemptDirectory) { throw 'Run this helper through m1-7-attempt.ps1.' }
$arguments = @(ConvertFrom-Json -InputObject $ArgumentsJson | ForEach-Object { [string]$_ })
$stdoutPath = Join-Path $attemptDirectory 'detached.stdout.log'
$stderrPath = Join-Path $attemptDirectory 'detached.stderr.log'
if ((Test-Path -LiteralPath $stdoutPath) -or (Test-Path -LiteralPath $stderrPath)) {
    throw 'Refusing to overwrite an existing detached-process log.'
}
$process = Start-Process -FilePath (Resolve-Path -LiteralPath $Executable).Path `
    -ArgumentList $arguments `
    -WorkingDirectory (Resolve-Path -LiteralPath $WorkingDirectory).Path `
    -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru
Start-Sleep -Milliseconds 500
$process.Refresh()
if ($process.HasExited) {
    throw "Background process exited during startup with code $($process.ExitCode)."
}
$result = [ordered]@{ status='started'; pid=$process.Id; stdout='detached.stdout.log'; stderr='detached.stderr.log' }
[IO.File]::WriteAllText((Join-Path $attemptDirectory 'background-process.json'), ($result | ConvertTo-Json -Compress) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
