$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$checked = @()
Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' | ForEach-Object {
    $tokens = $null; $parseErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile($_.FullName, [ref]$tokens, [ref]$parseErrors) | Out-Null
    if ($parseErrors.Count) { throw "PowerShell parse failure: $($_.Name)" }
    $checked += $_.Name
}
Get-ChildItem -LiteralPath $PSScriptRoot -Filter 'm17*.py' | ForEach-Object {
    & 'C:/Program Files/Python312/python.exe' -c 'import ast,pathlib,sys; ast.parse(pathlib.Path(sys.argv[1]).read_text())' $_.FullName
    if ($LASTEXITCODE -ne 0) { throw "Python parse failure: $($_.Name)" }
    $checked += $_.Name
}
$javascript = @('web/app.js','web/library.js','web/user-library.js','tools/m17-decode-corpus.js')
foreach ($relative in $javascript) {
    $file = Join-Path $root $relative
    if (Test-Path -LiteralPath $file) {
        & 'C:/Program Files/nodejs/node.exe' --check $file
        if ($LASTEXITCODE -ne 0) { throw "JavaScript parse failure: $relative" }
        $checked += $relative
    }
}
@{status='pass'; parsed_files=$checked; note='Syntax checks; behavior is covered by separately recorded campaigns.'} | ConvertTo-Json -Depth 4
