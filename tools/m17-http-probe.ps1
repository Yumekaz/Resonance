param(
    [string]$BaseUrl = 'http://127.0.0.1:18080',
    [Parameter(Mandatory=$true)][string]$RootId,
    [string]$ExpectedTitlesBase64 = ''
)

$ErrorActionPreference = 'Stop'
$expectedText = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($ExpectedTitlesBase64))
$ExpectedTitles = @($expectedText.Split('|') | Where-Object { $_ })
$health = Invoke-RestMethod -Uri "$BaseUrl/health" -TimeoutSec 5
$ready = Invoke-RestMethod -Uri "$BaseUrl/ready" -TimeoutSec 5
$tracksResponse = Invoke-RestMethod -Uri "$BaseUrl/api/v1/tracks?limit=50" -TimeoutSec 5
$artistsResponse = Invoke-RestMethod -Uri "$BaseUrl/api/v1/artists?limit=50" -TimeoutSec 5
$status = Invoke-RestMethod -Uri "$BaseUrl/api/v1/library/status" -TimeoutSec 5
if ($health.status -ne 'ok' -or $ready.status -ne 'ready' -or $ready.catalog -ne 'ready') {
    throw 'Health or readiness response did not match the M1 contract.'
}
$root = @($status.roots | Where-Object { $_.id -eq $RootId }) | Select-Object -First 1
if (-not $root -or $root.verification_state -ne 'verified' -or $root.watch_state -ne 'watching' -or -not $root.absence_reconciled) {
    throw 'The expected verified root is not actively watched after authoritative scan.'
}
$titles = @($tracksResponse.items | ForEach-Object { $_.title } | Where-Object { $_ })
foreach ($title in $ExpectedTitles) {
    if ($titles -notcontains $title) { throw "Expected imported Track '$title' was not returned by the catalog API." }
}
$artistCredits = @($artistsResponse.items | ForEach-Object { $_.display_credit })
if ($artistCredits -notcontains 'Browser Artist' -or $artistCredits -notcontains 'FLAC Artist') {
    throw 'Expected real Artist groups were not returned by the catalog API.'
}
$serialized = $status | ConvertTo-Json -Depth 20 -Compress
foreach ($unsafeField in @('canonical_path', 'local_path', 'native_identity', 'database_url')) {
    if ($serialized.ToLowerInvariant().Contains($unsafeField)) { throw "Path or connection detail field exposed: $unsafeField" }
}
[ordered]@{
    health = $health.status
    readiness = $ready.status
    catalog_state = $ready.catalog
    track_count_in_page = @($tracksResponse.items).Count
    artist_count_in_page = @($artistsResponse.items).Count
    expected_titles_present = $ExpectedTitles
    root_verification_state = $root.verification_state
    root_watch_state = $root.watch_state
    root_dirty = $root.dirty
    last_scan_status = $root.last_scan_status
    absence_reconciled = $root.absence_reconciled
    path_free_status = $true
} | ConvertTo-Json -Depth 5 -Compress
