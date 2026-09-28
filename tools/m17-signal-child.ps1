param([Parameter(Mandatory=$true)][int]$TargetProcessId)
$ErrorActionPreference='Stop'
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class M17ConsoleSignal {
 public delegate bool Handler(uint signal);
 public static Handler Ignore = delegate(uint signal) { return true; };
 [DllImport("kernel32.dll",SetLastError=true)] public static extern bool FreeConsole();
 [DllImport("kernel32.dll",SetLastError=true)] public static extern bool AttachConsole(uint id);
 [DllImport("kernel32.dll",SetLastError=true)] public static extern uint GetConsoleProcessList([Out] uint[] processes,uint size);
 [DllImport("kernel32.dll",SetLastError=true)] public static extern bool SetConsoleCtrlHandler(Handler handler,bool add);
 [DllImport("kernel32.dll",SetLastError=true)] public static extern bool GenerateConsoleCtrlEvent(uint type,uint group);
}
'@
[void][M17ConsoleSignal]::FreeConsole()
if(-not [M17ConsoleSignal]::AttachConsole([uint32]$TargetProcessId)){throw "Cannot attach to the test child's console: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"}
$consoleProcesses=[uint32[]]::new(32)
$count=[M17ConsoleSignal]::GetConsoleProcessList($consoleProcesses,32)
if($count-eq 0-or $count-gt 32){[void][M17ConsoleSignal]::FreeConsole();throw 'Cannot prove the test console process boundary.'}
for($i=0;$i-lt $count;$i++){if($consoleProcesses[$i]-ne [uint32]$TargetProcessId-and $consoleProcesses[$i]-ne [uint32]$PID){[void][M17ConsoleSignal]::FreeConsole();throw 'The test console contains an unrelated process; refusing a console signal.'}}
[void][M17ConsoleSignal]::SetConsoleCtrlHandler([M17ConsoleSignal]::Ignore,$true)
if(-not [M17ConsoleSignal]::GenerateConsoleCtrlEvent(1,0)){throw "Cannot signal the test child: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"}
Start-Sleep -Milliseconds 500
[void][M17ConsoleSignal]::FreeConsole()
