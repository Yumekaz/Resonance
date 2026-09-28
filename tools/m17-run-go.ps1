param(
 [Parameter(Mandatory=$true)][string]$Campaign,
 [Parameter(Mandatory=$true)][string]$ArgumentsJson,
 [hashtable]$ExtraEnvironment=@{},
 [string]$PasswordFile='',
 [int]$TimeoutSeconds=1800
)
$ErrorActionPreference='Stop'
$repo=(Split-Path -Parent $PSScriptRoot)
$go=Join-Path $repo 'data/gopath/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.0.windows-amd64/bin/go.exe'
$pgbin=Join-Path $repo 'data/postgresql-17.11/pgsql/bin'
if(-not $PasswordFile){$PasswordFile=Join-Path $env:TEMP 'ResonanceM17G1-20260928/postgres-password.txt'}
$password=(Get-Content -LiteralPath $PasswordFile -Raw).Trim()
$envs=@{
 GOROOT=(Split-Path -Parent (Split-Path -Parent $go));GOPATH=(Join-Path $repo 'data/gopath');GOMODCACHE=(Join-Path $repo 'data/gopath/pkg/mod');GOCACHE=(Join-Path $repo 'data/go-cache');GOTOOLCHAIN='local';GOPROXY='off'
 SystemRoot=$env:SystemRoot;TEMP=$env:TEMP;TMP=$env:TMP
 PATH="$(Split-Path -Parent $go);$pgbin;$env:SystemRoot\System32;$env:SystemRoot\System32\Wbem;$env:SystemRoot\System32\WindowsPowerShell\v1.0"
 RESONANCE_TEST_DATABASE_URL="host=127.0.0.1 port=55439 user=resonance dbname=resonance_m17_test password=$password sslmode=disable"
 RESONANCE_M17_ATTEMPT_OUTPUT='%M17_ATTEMPT_DIR%'
}
foreach($key in $ExtraEnvironment.Keys){$envs[$key]=$ExtraEnvironment[$key]}
& (Join-Path $PSScriptRoot 'm1-7-attempt.ps1') -Campaign $Campaign -Executable $go -ArgumentsJson $ArgumentsJson -Environment $envs -ClearEnvironment -TimeoutSeconds $TimeoutSeconds -WorkingDirectory $repo
if($LASTEXITCODE -ne 0){throw "Recorded Go campaign failed: $Campaign. Its raw attempt is preserved."}
