$ErrorActionPreference='Stop'
$attempt=$env:RESONANCE_M17_ATTEMPT_DIR
if(-not $attempt){throw 'Use the M1.7 attempt recorder.'}
$probe=Join-Path (Split-Path -Parent $PSScriptRoot) ('data/m17-closure/symlink-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $probe | Out-Null
$target=Join-Path $probe 'target.txt'
$link=Join-Path $probe 'link.txt'
[IO.File]::WriteAllText($target,'private synthetic fixture')
Add-Type -TypeDefinition @'
using System.Runtime.InteropServices;
public static class M17Symlink {
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)]
 [return:MarshalAs(UnmanagedType.U1)]
 public static extern bool CreateSymbolicLink(string link,string target,uint flags);
}
'@
$created=[M17Symlink]::CreateSymbolicLink($link,$target,2)
$errorCode=if($created){0}else{[Runtime.InteropServices.Marshal]::GetLastWin32Error()}
$result=[ordered]@{created=$created;win32_error=$errorCode;used_allow_unprivileged_create=$true;requirement=if($created){'none'}else{'Run tests in an elevated Administrator console with SeCreateSymbolicLinkPrivilege, or enable Windows Developer Mode for unprivileged symbolic link creation. No system policy was changed.'}}
[IO.File]::WriteAllText((Join-Path $attempt 'symlink-capability.json'),($result|ConvertTo-Json),[Text.UTF8Encoding]::new($false))
$result|ConvertTo-Json -Compress
whoami /priv
if(-not $created){exit 2}
