param(
    [string]$BaseUrl = 'http://127.0.0.1:18083',
    [Parameter(Mandatory=$true)][string]$RootId,
    [string]$PrivatePathCanary = ''
)

$ErrorActionPreference = 'Stop'
$ready = Invoke-RestMethod "$BaseUrl/ready" -TimeoutSec 5
if ($ready.status -ne 'ready' -or $ready.catalog -ne 'ready') { throw 'Restored application readiness failed.' }
$catalog = Invoke-RestMethod "$BaseUrl/api/v1/tracks?limit=50" -TimeoutSec 5
$browser = @($catalog.items | Where-Object { $_.title -eq 'Browser Song' -and $_.artist_credit -eq 'Browser Artist' }) | Select-Object -First 1
$short = @($catalog.items | Where-Object { $_.title -eq 'Short' }) | Select-Object -First 1
if (-not $browser -or -not $short -or $short.available) { throw 'Restored catalog did not preserve the available Browser Track and unavailable Short Track.' }

$queue = Invoke-RestMethod "$BaseUrl/api/v1/queue" -TimeoutSec 5
$queueOccurrences = @($queue.items | Where-Object { $_.track_id -eq $browser.id })
if ($queueOccurrences.Count -ne 2 -or (@($queueOccurrences.id | Select-Object -Unique).Count -ne 2)) { throw 'Restored queue occurrences are missing or collapsed.' }

$playlists = Invoke-RestMethod "$BaseUrl/api/v1/playlists?limit=200" -TimeoutSec 5
$playlist = @($playlists.items | Where-Object { $_.name -eq 'M17 restore evidence' }) | Select-Object -First 1
if (-not $playlist) { throw 'Restored playlist is missing.' }
$detail = Invoke-RestMethod "$BaseUrl/api/v1/playlists/$($playlist.id)" -TimeoutSec 5
if ($detail.items.Count -ne 2 -or @($detail.items.id | Select-Object -Unique).Count -ne 2 -or @($detail.items | Where-Object { $_.track_id -eq $browser.id }).Count -ne 2) {
    throw 'Restored playlist occurrences are missing or collapsed.'
}

$favorites = Invoke-RestMethod "$BaseUrl/api/v1/favorites?limit=200" -TimeoutSec 5
if (@($favorites.items | Where-Object { $_.track_id -eq $browser.id }).Count -ne 1) { throw 'Restored favorite is missing.' }
$history = Invoke-RestMethod "$BaseUrl/api/v1/history?limit=200" -TimeoutSec 5
if (@($history.items).Count -eq 0) { throw 'Restored listening history is empty.' }
$status = Invoke-RestMethod "$BaseUrl/api/v1/library/status" -TimeoutSec 5
$root = @($status.roots | Where-Object { $_.id -eq $RootId }) | Select-Object -First 1
if (-not $root -or $root.verification_state -ne 'verified' -or $root.watch_state -ne 'watching') { throw 'Restored root did not become verified and watched.' }

$request = [System.Net.HttpWebRequest]::Create([Uri]"$BaseUrl$($browser.stream_url)")
$request.Timeout = 15000
$request.AddRange(0, 1023)
$range = $request.GetResponse()
$rangeStream = $range.GetResponseStream()
$rangeBuffer = [byte[]]::new(1024)
$rangeBytes = $rangeStream.Read($rangeBuffer, 0, $rangeBuffer.Length)
$rangeContent = [string]$range.Headers['Content-Range']
if ($range.StatusCode -ne [System.Net.HttpStatusCode]::PartialContent -or $rangeContent -notmatch '^bytes 0-1023/') { throw 'Restored Track Range playback probe failed.' }
$rangeStream.Dispose()
$range.Dispose()
$serialized = (@($status,$queue,$playlists,$detail,$favorites,$history) | ConvertTo-Json -Depth 24 -Compress).ToLowerInvariant()
foreach ($forbidden in @('canonical_path','local_path','native_identity','database_url')) {
    if ($serialized.Contains($forbidden)) { throw "Restored API exposed forbidden field: $forbidden" }
}
if ($PrivatePathCanary -and $serialized.Contains($PrivatePathCanary.ToLowerInvariant())) { throw 'Restored API exposed the private root path canary.' }
[ordered]@{
    readiness = $ready.status
    catalog_track_count = @($catalog.items).Count
    short_track_retained_unavailable = (-not $short.available)
    queue_duplicate_occurrences = $queueOccurrences.Count
    playlist_duplicate_occurrences = $detail.items.Count
    favorite_present = $true
    history_rows = @($history.items).Count
    root_watch_state = $root.watch_state
    range_status = [int]206
    range_content_range = $rangeContent
    range_bytes = $rangeBytes
    public_data_path_free = $true
} | ConvertTo-Json -Compress
