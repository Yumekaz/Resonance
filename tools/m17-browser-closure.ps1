param([Parameter(Mandatory=$true)][string]$CorpusDirectory)
$ErrorActionPreference='Stop'
$attempt=$env:RESONANCE_M17_ATTEMPT_DIR
if(-not $attempt -or -not $env:PGPASSWORD){throw 'Use the attempt recorder with the disposable PostgreSQL password supplied through the environment.'}
$repo=Split-Path -Parent $PSScriptRoot
$exe=Join-Path $repo 'data/m17-closure/resonance.exe'
$pgbin=Join-Path $repo 'data/postgresql-17.11/pgsql/bin'
$rootPath=Join-Path (Resolve-Path -LiteralPath $CorpusDirectory).Path 'library'
$media=Join-Path $rootPath 'long.wav'
$node=(Get-Command node.exe).Source
$database='resonance_m17_closure_browser_'+[guid]::NewGuid().ToString('N').Substring(0,12)
$dsn="host=127.0.0.1 port=55439 user=resonance dbname=$database password=$env:PGPASSWORD sslmode=disable"
$demo=$null;$configured=$null
function Invoke-Native([string]$Executable,[string[]]$Arguments,[string]$Label){
 $info=[Diagnostics.ProcessStartInfo]::new();$info.FileName=$Executable;$info.WorkingDirectory=$repo;$info.UseShellExecute=$false;$info.CreateNoWindow=$true;$info.RedirectStandardOutput=$true;$info.RedirectStandardError=$true
 $info.Arguments=($Arguments|ForEach-Object{'"'+$_.Replace('"','\"')+'"'}) -join ' '
 $p=[Diagnostics.Process]::new();$p.StartInfo=$info;$started=[DateTimeOffset]::UtcNow
 if(-not $p.Start()){throw 'Child command failed to start'}
 $stdout=$p.StandardOutput.ReadToEndAsync();$stderr=$p.StandardError.ReadToEndAsync();$p.WaitForExit()
 $out=$stdout.GetAwaiter().GetResult();$err=$stderr.GetAwaiter().GetResult()
 [IO.File]::WriteAllText((Join-Path $attempt "$Label.stdout.log"),$out,[Text.UTF8Encoding]::new($false))
 [IO.File]::WriteAllText((Join-Path $attempt "$Label.stderr.log"),$err,[Text.UTF8Encoding]::new($false))
 [ordered]@{label=$Label;executable=(Split-Path -Leaf $Executable);arguments=$Arguments;started_utc=$started.ToString('o');finished_utc=[DateTimeOffset]::UtcNow.ToString('o');exit_code=$p.ExitCode}|ConvertTo-Json -Compress|Add-Content (Join-Path $attempt 'commands.jsonl')
 if($p.ExitCode-ne 0){throw "$Label failed; its raw logs are preserved."}
 return $out
}
function Wait-Health([string]$Url){for($i=0;$i-lt 100;$i++){try{$r=Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2;if($r.StatusCode-eq 200){return}}catch{};Start-Sleep -Milliseconds 100};throw 'Test server did not become ready.'}
try{
 [void](Invoke-Native (Join-Path $pgbin 'psql.exe') @('-X','-w','-v','ON_ERROR_STOP=1','-h','127.0.0.1','-p','55439','-U','resonance','-d','postgres','-c',"CREATE DATABASE $database OWNER resonance") 'create-database')
 $env:RESONANCE_DATABASE_URL=$dsn
 [void](Invoke-Native $exe @('-migrate-only') 'migrate')
 $rootJson=Invoke-Native $exe @('library','add','-path',$rootPath,'-name','M17 closure browser') 'enroll'
 $rootId=($rootJson|ConvertFrom-Json).id
 [void](Invoke-Native $exe @('library','verify',$rootId) 'verify')
 [void](Invoke-Native $exe @('library','enable',$rootId) 'enable')
 [void](Invoke-Native $exe @('library','scan',$rootId) 'scan')
 Remove-Item Env:RESONANCE_DATABASE_URL
 $demo=Start-Process -FilePath $exe -ArgumentList @('-addr','127.0.0.1:18086','-media',('"'+$media+'"')) -WindowStyle Hidden -RedirectStandardOutput (Join-Path $attempt 'demo-server.stdout.log') -RedirectStandardError (Join-Path $attempt 'demo-server.stderr.log') -PassThru
 Wait-Health 'http://127.0.0.1:18086/health'
 $env:RESONANCE_DATABASE_URL=$dsn
 $configured=Start-Process -FilePath $exe -ArgumentList @('-addr','127.0.0.1:18087','-media',('"'+$media+'"')) -WindowStyle Hidden -RedirectStandardOutput (Join-Path $attempt 'catalog-server.stdout.log') -RedirectStandardError (Join-Path $attempt 'catalog-server.stderr.log') -PassThru
 Wait-Health 'http://127.0.0.1:18087/ready'
 $env:RESONANCE_E2E_BASE_URL='http://127.0.0.1:18086'
 $env:PLAYWRIGHT_JSON_OUTPUT_NAME=Join-Path $attempt 'm0-report.json'
 [void](Invoke-Native $node @('node_modules/@playwright/test/cli.js','test','tests/e2e/player.spec.js','--workers=1','--reporter=line,json','--output',(Join-Path $attempt 'm0-test-results')) 'm0-chrome')
 $env:RESONANCE_E2E_BASE_URL='http://127.0.0.1:18087'
 $env:RESONANCE_M14_E2E='1';$env:RESONANCE_M15_E2E='1';$env:RESONANCE_M16_E2E='1';$env:RESONANCE_M17_E2E='1';$env:RESONANCE_M16_ROOT_ID=$rootId
 $env:PLAYWRIGHT_JSON_OUTPUT_NAME=Join-Path $attempt 'configured-report.json'
 [void](Invoke-Native $node @('node_modules/@playwright/test/cli.js','test','tests/e2e/library.spec.js','tests/e2e/user-library.spec.js','tests/e2e/library-status.spec.js','tests/e2e/m17-journey.spec.js','--workers=1','--reporter=line,json','--output',(Join-Path $attempt 'configured-test-results')) 'configured-chrome')
 $m0=Get-Content (Join-Path $attempt 'm0-report.json') -Raw|ConvertFrom-Json
 $catalog=Get-Content (Join-Path $attempt 'configured-report.json') -Raw|ConvertFrom-Json
 if($m0.stats.expected-ne 3-or $m0.stats.unexpected-ne 0-or $m0.stats.skipped-ne 0-or $catalog.stats.expected-ne 15-or $catalog.stats.unexpected-ne 0-or $catalog.stats.skipped-ne 0){throw 'Browser counts did not satisfy the final regression contract.'}
 [ordered]@{m0=$m0.stats;configured=$catalog.stats;root_id=$rootId;fresh_disposable_database=$true;fresh_browser_profiles=$true}|ConvertTo-Json -Depth 6|Set-Content (Join-Path $attempt 'browser-summary.json') -Encoding utf8
 'PASS: M0 3/3 and configured M1.4-M1.7 15/15; all required browser gates enabled.'
}finally{
 foreach($p in @($demo,$configured)){if($p){$p.Refresh();if(-not $p.HasExited){Stop-Process -Id $p.Id -Force}}}
}
