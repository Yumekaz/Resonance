param(
    [string]$OutputDir = 'data/m1-2-benchmark-root',
    [ValidateRange(1, 1000)][int]$MP3Count = 200
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$sourceMP3 = Join-Path $repoRoot 'testdata/metadata/untagged.mp3'
$sourceFLAC = Join-Path $repoRoot 'testdata/metadata/untagged.flac'
$destination = [System.IO.Path]::GetFullPath((Join-Path (Get-Location) $OutputDir))
if (Test-Path -LiteralPath $destination) {
    if (Get-ChildItem -LiteralPath $destination -Force | Select-Object -First 1) {
        throw 'Benchmark output directory must be absent or empty so stale files cannot change the corpus.'
    }
} else {
    New-Item -ItemType Directory -Path $destination | Out-Null
}

for ($i = 1; $i -le $MP3Count; $i++) {
    $target = Join-Path $destination ('track-{0:D4}.mp3' -f $i)
    Copy-Item -LiteralPath $sourceMP3 -Destination $target -Force
    # A distinct ID3v1 title changes encoded bytes without changing the CC0 audio.
    $tag = [byte[]]::new(128)
    [System.Text.Encoding]::ASCII.GetBytes('TAG').CopyTo($tag, 0)
    [System.Text.Encoding]::ASCII.GetBytes(('Benchmark {0:D4}' -f $i)).CopyTo($tag, 3)
    [System.Text.Encoding]::ASCII.GetBytes('Generated Corpus').CopyTo($tag, 33)
    $stream = [System.IO.File]::Open($target, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write)
    try { $stream.Write($tag, 0, $tag.Length) } finally { $stream.Dispose() }
}
Copy-Item -LiteralPath $sourceFLAC -Destination (Join-Path $destination 'sample.flac') -Force

$files = Get-ChildItem -LiteralPath $destination -File
[pscustomobject]@{
    output_dir = $destination
    files = $files.Count
    bytes = ($files | Measure-Object Length -Sum).Sum
    mp3_count = $MP3Count
    flac_count = 1
} | ConvertTo-Json -Compress
