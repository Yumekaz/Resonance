param(
    [string]$BaseUrl = 'http://127.0.0.1:18081',
    [Parameter(Mandatory=$true)][string]$LibraryRoot,
    [Parameter(Mandatory=$true)][string]$UnavailableCopy
)

$ErrorActionPreference = 'Stop'
function Invoke-Json([string]$Method, [string]$Path, [object]$Body = $null, [string]$Key = '') {
    $headers = @{}
    if ($Key) { $headers['Idempotency-Key'] = $Key }
    if ($null -eq $Body) {
        return Invoke-RestMethod -Method $Method -Uri "$BaseUrl$Path" -Headers $headers -TimeoutSec 10
    }
    return Invoke-RestMethod -Method $Method -Uri "$BaseUrl$Path" -Headers $headers -ContentType 'application/json' -Body ($Body | ConvertTo-Json -Depth 8 -Compress) -TimeoutSec 10
}

$started = [DateTimeOffset]::UtcNow
$catalog = Invoke-Json 'GET' '/api/v1/tracks?limit=50'
$browser = @($catalog.items | Where-Object { $_.title -eq 'Browser Song' -and $_.artist_credit -eq 'Browser Artist' }) | Select-Object -First 1
$short = @($catalog.items | Where-Object { $_.title -eq 'Short' }) | Select-Object -First 1
if (-not $browser -or -not $short) { throw 'Expected generated Browser Song and Short Tracks are missing.' }

$shortPath = Join-Path $LibraryRoot 'Short.wav'
if (-not (Test-Path -LiteralPath $UnavailableCopy)) { New-Item -ItemType Directory -Force -Path (Split-Path -Parent $UnavailableCopy) | Out-Null; Copy-Item -LiteralPath $shortPath -Destination $UnavailableCopy }
Remove-Item -LiteralPath $shortPath
$deadline = [DateTimeOffset]::UtcNow.AddSeconds(25)
$shortUnavailable = $false
while ([DateTimeOffset]::UtcNow -lt $deadline) {
    $tracks = Invoke-Json 'GET' '/api/v1/tracks?limit=50'
    $shortNow = @($tracks.items | Where-Object { $_.id -eq $short.id }) | Select-Object -First 1
    if ($shortNow -and -not $shortNow.available) { $shortUnavailable = $true; break }
    Start-Sleep -Milliseconds 300
}
if (-not $shortUnavailable) { throw 'Watcher did not retain the Track and mark its removed location unavailable.' }

$queue = Invoke-Json 'GET' '/api/v1/queue'
$null = Invoke-Json 'DELETE' '/api/v1/queue' @{ expected_version = $queue.revision } ([guid]::NewGuid().ToString())
$queueIds = @()
for ($i = 0; $i -lt 2; $i++) {
    $queue = Invoke-Json 'GET' '/api/v1/queue'
    $change = Invoke-Json 'POST' '/api/v1/queue/items' @{ track_id = $browser.id; placement = 'end'; expected_version = $queue.revision } ([guid]::NewGuid().ToString())
    $queueIds += $change.item_id
}
if ($queueIds.Count -ne 2 -or $queueIds[0] -eq $queueIds[1]) { throw 'Duplicate queue occurrences did not receive distinct durable item IDs.' }

$playlist = Invoke-Json 'POST' '/api/v1/playlists' @{ name = 'M17 restore evidence'; expected_version = 0 } ([guid]::NewGuid().ToString())
for ($i = 0; $i -lt 2; $i++) {
    $detail = Invoke-Json 'GET' "/api/v1/playlists/$($playlist.id)"
    $null = Invoke-Json 'POST' "/api/v1/playlists/$($playlist.id)/items" @{ track_id = $browser.id; expected_version = $detail.revision } ([guid]::NewGuid().ToString())
}
$playlistDetail = Invoke-Json 'GET' "/api/v1/playlists/$($playlist.id)"
if ($playlistDetail.items.Count -ne 2 -or $playlistDetail.items[0].id -eq $playlistDetail.items[1].id) { throw 'Duplicate playlist occurrences were not retained.' }
$null = Invoke-Json 'PUT' "/api/v1/favorites/tracks/$($browser.id)"

$result = [ordered]@{
    unavailable_track_retained = ($shortNow.id -eq $short.id)
    unavailable_track_available = $shortNow.available
    queue_item_count = $queueIds.Count
    queue_item_ids_distinct = ($queueIds[0] -ne $queueIds[1])
    playlist_item_count = $playlistDetail.items.Count
    playlist_item_ids_distinct = ($playlistDetail.items[0].id -ne $playlistDetail.items[1].id)
    favorite_track_id = $browser.id
    playlist_id = $playlist.id
    duration_ms = [Math]::Round(([DateTimeOffset]::UtcNow - $started).TotalMilliseconds, 1)
    unavailable_relative_path = 'library/Short.wav'
}
$result | ConvertTo-Json -Depth 5 -Compress
