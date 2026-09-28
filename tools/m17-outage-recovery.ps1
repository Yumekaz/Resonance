param(
    [string]$BaseUrl = 'http://127.0.0.1:18083',
    [Parameter(Mandatory=$true)][string]$ClusterPath,
    [Parameter(Mandatory=$true)][string]$PostgresBin,
    [Parameter(Mandatory=$true)][string]$LibraryRoot,
    [string]$Port = '55439'
)

$ErrorActionPreference = 'Stop'
$attemptDirectory = $env:RESONANCE_M17_ATTEMPT_DIR
if (-not $attemptDirectory) { throw 'Run this drill through m1-7-attempt.ps1.' }
$state = [ordered]@{ result='in_progress'; started_utc=[DateTimeOffset]::UtcNow.ToString('o') }
$pgStopped = $false
$response = $null
$responseStream = $null
$output = $null
$downloadPath = Join-Path $env:TEMP ('m17-outage-' + [guid]::NewGuid().ToString('N') + '.wav')
$recoveryStdout = Join-Path $attemptDirectory 'postgres-recovery.stdout.log'
$recoveryStderr = Join-Path $attemptDirectory 'postgres-recovery.stderr.log'

function Call-Api([string]$Method, [string]$Path, [object]$Body = $null, [string]$Key = '') {
    $request = [System.Net.HttpWebRequest]::Create([Uri]"$BaseUrl$Path")
    $request.Method = $Method
    $request.Timeout = 7000
    $request.ReadWriteTimeout = 7000
    if ($Key) { $request.Headers.Add('Idempotency-Key', $Key) }
    if ($null -ne $Body) {
        $request.ContentType = 'application/json'
        $bytes = [Text.Encoding]::UTF8.GetBytes(($Body | ConvertTo-Json -Depth 8 -Compress))
        $request.ContentLength = $bytes.Length
        $stream = $request.GetRequestStream()
        try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
    }
    try { $httpResponse = $request.GetResponse() }
    catch [System.Net.WebException] {
        $httpResponse = $_.Exception.Response
        if (-not $httpResponse) { throw }
    }
    $reader = [IO.StreamReader]::new($httpResponse.GetResponseStream())
    try { $content = $reader.ReadToEnd() } finally { $reader.Dispose() }
    $parsed = $null
    try { if ($content) { $parsed = ConvertFrom-Json -InputObject $content } } catch { }
    $result = [pscustomobject]@{ Status=[int]$httpResponse.StatusCode; Content=$parsed; Text=$content }
    $httpResponse.Dispose()
    return $result
}

function Start-Database([string]$Reason) {
    $postgres = Join-Path $PostgresBin 'postgres.exe'
    $pidFile = Join-Path $ClusterPath 'postmaster.pid'
    if (Test-Path -LiteralPath $pidFile) {
        $oldPid = 0
        if (-not [int]::TryParse((Get-Content -LiteralPath $pidFile -TotalCount 1), [ref]$oldPid)) { throw 'Stale PostgreSQL PID marker could not be parsed.' }
        $oldProcess = Get-Process -Id $oldPid -ErrorAction SilentlyContinue
        if ($oldProcess -and $oldProcess.Path -ine (Resolve-Path -LiteralPath $postgres).Path) { $oldProcess = $null }
        $readiness = Call-Api 'GET' '/ready'
        if ($oldProcess -or $readiness.Status -eq 200) { throw 'Refusing to clear a live PostgreSQL PID marker.' }
        Remove-Item -LiteralPath $pidFile
        [IO.File]::WriteAllText((Join-Path $attemptDirectory 'stale-postmaster-pid-cleanup.log'), 'Old PID was absent and application readiness returned unavailable; removed one stale marker.' + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    }
    $arguments = @('-D', $ClusterPath, '-p', $Port, '-h', '127.0.0.1', '-c', 'max_connections=30')
    $process = Start-Process -FilePath $postgres -ArgumentList $arguments -WorkingDirectory $PostgresBin `
        -WindowStyle Hidden -RedirectStandardOutput $recoveryStdout -RedirectStandardError $recoveryStderr -PassThru
    $deadline = [DateTimeOffset]::UtcNow.AddSeconds(45)
    while ([DateTimeOffset]::UtcNow -lt $deadline) {
        $readiness = Call-Api 'GET' '/ready'
        if ($readiness.Status -eq 200) { return $process }
        Start-Sleep -Milliseconds 250
    }
    throw "PostgreSQL did not recover after $Reason."
}

try {
    $catalog = (Call-Api 'GET' '/api/v1/tracks?limit=50').Content
    $longTrack = @($catalog.items | Where-Object { $_.title -eq 'long' }) | Select-Object -First 1
    $browserTrack = @($catalog.items | Where-Object { $_.title -eq 'Browser Song' -and $_.artist_credit -eq 'Browser Artist' }) | Select-Object -First 1
    $shortTrack = @($catalog.items | Where-Object { $_.title -eq 'Short' }) | Select-Object -First 1
    if (-not $longTrack -or -not $browserTrack -or -not $shortTrack) { throw 'Required generated Tracks were missing before outage.' }

    $sessionId = [guid]::NewGuid().ToString()
    $sessionStart = Call-Api 'POST' '/api/v1/listening-sessions' @{ id=$sessionId; track_id=$browserTrack.id; client_instance_id=[guid]::NewGuid().ToString() }
    if ($sessionStart.Status -lt 200 -or $sessionStart.Status -ge 300) { throw 'Synthetic report session setup failed.' }
    $reportBody = @{ sequence=1; listened_ms=5000; position_ms=5000; duration_ms=300000; seek_count=0 }
    $queueBefore = (Call-Api 'GET' '/api/v1/queue').Content
    $queueBody = @{ track_id=$browserTrack.id; placement='end'; expected_version=$queueBefore.revision }
    $queueKey = [guid]::NewGuid().ToString()
    $playlistName = 'M17 outage retry ' + [guid]::NewGuid().ToString('N').Substring(0, 8)
    $playlistBody = @{ name=$playlistName; expected_version=0 }
    $playlistKey = [guid]::NewGuid().ToString()

    $streamRequest = [System.Net.HttpWebRequest]::Create([Uri]"$BaseUrl$($longTrack.stream_url)")
    $streamRequest.Method = 'GET'
    $streamRequest.Timeout = 10000
    $streamRequest.ReadWriteTimeout = 30000
    $response = $streamRequest.GetResponse()
    $expectedBytes = [int64]$response.ContentLength
    $streamStatus = [int]$response.StatusCode
    if ($streamStatus -ne 200 -or $expectedBytes -lt 20MB) { throw 'Indexed stream did not open with the expected full representation.' }
    $responseStream = $response.GetResponseStream()
    $output = [IO.FileStream]::new($downloadPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
    $buffer = [byte[]]::new(65536)
    $bytesBeforeStop = $responseStream.Read($buffer, 0, $buffer.Length)
    if ($bytesBeforeStop -le 0) { throw 'Indexed stream returned no media bytes before outage.' }
    $output.Write($buffer, 0, $bytesBeforeStop)

    $clusterPidFile = Join-Path $ClusterPath 'postmaster.pid'
    $postmasterPID = [int](Get-Content -LiteralPath $clusterPidFile -TotalCount 1)
    $postmaster = Get-Process -Id $postmasterPID -ErrorAction Stop
    $postgresExecutable = (Resolve-Path -LiteralPath (Join-Path $PostgresBin 'postgres.exe')).Path
    if ($postmaster.ProcessName -ne 'postgres' -or $postmaster.Path -ine $postgresExecutable) { throw 'PID fence did not match the disposable PostgreSQL server.' }
    Stop-Process -Id $postmasterPID -Force
    $pgStopped = $true
    $stopDeadline = [DateTimeOffset]::UtcNow.AddSeconds(15)
    do {
        $readiness = Call-Api 'GET' '/ready'
        if ($readiness.Status -eq 503) { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTimeOffset]::UtcNow -lt $stopDeadline)
    if (-not $readiness -or $readiness.Status -ne 503) { throw 'PostgreSQL-backed readiness did not fail after its owned process was stopped.' }

    $health = Call-Api 'GET' '/health'
    $readyOutage = Call-Api 'GET' '/ready'
    $catalogOutage = Call-Api 'GET' '/api/v1/tracks?limit=1'
    $queueReadOutage = Call-Api 'GET' '/api/v1/queue'
    $queueWriteOutage = Call-Api 'POST' '/api/v1/queue/items' $queueBody $queueKey
    $playlistWriteOutage = Call-Api 'POST' '/api/v1/playlists' $playlistBody $playlistKey
    $favoriteWriteOutage = Call-Api 'PUT' "/api/v1/favorites/tracks/$($shortTrack.id)"
    $reportWriteOutage = Call-Api 'PUT' "/api/v1/listening-sessions/$sessionId/report" $reportBody
    if ($health.Status -ne 200 -or $readyOutage.Status -ne 503 -or $catalogOutage.Status -ne 503 -or $queueReadOutage.Status -ne 503 -or $queueWriteOutage.Status -ne 503 -or $playlistWriteOutage.Status -ne 503 -or $favoriteWriteOutage.Status -ne 503 -or $reportWriteOutage.Status -ne 503) {
        throw 'An outage response did not match liveness, readiness, or durable-write failure behavior.'
    }

    $bytesDuringOutage = 0
    while (($read = $responseStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
        $output.Write($buffer, 0, $read)
        $bytesDuringOutage += $read
    }
    $output.Flush()
    $totalBytes = $bytesBeforeStop + $bytesDuringOutage
    if ($totalBytes -ne $expectedBytes) { throw 'Already-open indexed media did not finish with its advertised byte count while PostgreSQL was down.' }
    $output.Dispose(); $output = $null
    $responseStream.Dispose(); $responseStream = $null
    $response.Dispose(); $response = $null

    $null = Start-Database 'the injected PostgreSQL outage'
    $pgStopped = $false
    $ready = $null
    $deadline = [DateTimeOffset]::UtcNow.AddSeconds(30)
    while ([DateTimeOffset]::UtcNow -lt $deadline) {
        $ready = Call-Api 'GET' '/ready'
        if ($ready.Status -eq 200) { break }
        Start-Sleep -Milliseconds 300
    }
    if (-not $ready -or $ready.Status -ne 200) { throw 'Application readiness did not recover after PostgreSQL restart.' }

    $queueBeforeRetry = (Call-Api 'GET' '/api/v1/queue').Content
    if ($queueBeforeRetry.items.Count -ne $queueBefore.items.Count) { throw 'Queue mutation partially committed during the outage.' }
    $playlistsBeforeRetry = (Call-Api 'GET' '/api/v1/playlists?limit=200').Content
    if (@($playlistsBeforeRetry.items | Where-Object { $_.name -eq $playlistName }).Count -ne 0) { throw 'Playlist mutation partially committed during the outage.' }
    $favoritesBeforeRetry = (Call-Api 'GET' '/api/v1/favorites?limit=200').Content
    if (@($favoritesBeforeRetry.items | Where-Object { $_.track_id -eq $shortTrack.id }).Count -ne 0) { throw 'Favorite mutation partially committed during the outage.' }

    $queueRetry = Call-Api 'POST' '/api/v1/queue/items' $queueBody $queueKey
    $queueReplay = Call-Api 'POST' '/api/v1/queue/items' $queueBody $queueKey
    $queueAfter = (Call-Api 'GET' '/api/v1/queue').Content
    $queueItemID = $queueRetry.Content.item_id
    if ($queueRetry.Status -lt 200 -or $queueRetry.Status -ge 300 -or $queueReplay.Content.item_id -ne $queueItemID -or $queueAfter.items.Count -ne ($queueBefore.items.Count + 1)) { throw 'Queue retry/replay did not create one item.' }

    $playlistRetry = Call-Api 'POST' '/api/v1/playlists' $playlistBody $playlistKey
    $playlistReplay = Call-Api 'POST' '/api/v1/playlists' $playlistBody $playlistKey
    if ($playlistRetry.Status -lt 200 -or $playlistRetry.Status -ge 300 -or $playlistReplay.Content.id -ne $playlistRetry.Content.id) { throw 'Playlist retry/replay did not return its original receipt.' }
    $favoriteRetry = Call-Api 'PUT' "/api/v1/favorites/tracks/$($shortTrack.id)"
    $favoriteReplay = Call-Api 'PUT' "/api/v1/favorites/tracks/$($shortTrack.id)"
    $reportRetry = Call-Api 'PUT' "/api/v1/listening-sessions/$sessionId/report" $reportBody
    $reportReplay = Call-Api 'PUT' "/api/v1/listening-sessions/$sessionId/report" $reportBody
    if ($favoriteRetry.Status -lt 200 -or $favoriteRetry.Status -ge 300 -or $favoriteReplay.Status -lt 200 -or $favoriteReplay.Status -ge 300 -or $reportRetry.Status -lt 200 -or $reportRetry.Status -ge 300 -or $reportReplay.Status -lt 200 -or $reportReplay.Status -ge 300) {
        throw 'A durable write retry failed after PostgreSQL recovery.'
    }

    $download = Get-Item -LiteralPath $downloadPath
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $downloadPath).Hash.ToLowerInvariant()
    $favoriteAfter = (Call-Api 'GET' '/api/v1/favorites?limit=200').Content
    $playlistAfter = (Call-Api 'GET' "/api/v1/playlists/$($playlistRetry.Content.id)").Content
    $state = [ordered]@{
        result='pass'
        health_status_during_outage=$health.Status
        readiness_status_during_outage=$readyOutage.Status
        catalog_read_status_during_outage=$catalogOutage.Status
        queue_read_status_during_outage=$queueReadOutage.Status
        queue_write_status_during_outage=$queueWriteOutage.Status
        playlist_write_status_during_outage=$playlistWriteOutage.Status
        favorite_write_status_during_outage=$favoriteWriteOutage.Status
        listening_report_status_during_outage=$reportWriteOutage.Status
        media_status_before_outage=$streamStatus
        media_bytes_before_outage=$bytesBeforeStop
        media_bytes_after_database_stop=$bytesDuringOutage
        media_bytes_total=$totalBytes
        expected_media_bytes=$expectedBytes
        media_sha256=$hash
        queue_items_before=$queueBefore.items.Count
        queue_items_after_retry_and_replay=$queueAfter.items.Count
        queue_receipt_replay_same_item=($queueReplay.Content.item_id -eq $queueItemID)
        playlist_receipt_replay_same_id=($playlistReplay.Content.id -eq $playlistRetry.Content.id)
        playlist_occurrences_preserved=$playlistAfter.items.Count
        favorite_after_retry=(@($favoriteAfter.items | Where-Object { $_.track_id -eq $shortTrack.id }).Count -eq 1)
        session_report_retry_status=$reportReplay.Status
        postgres_recovered=$true
    }
    [IO.File]::WriteAllText((Join-Path $attemptDirectory 'outage-result.json'), ($state | ConvertTo-Json -Depth 8 -Compress) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
} catch {
    [IO.File]::WriteAllText((Join-Path $attemptDirectory 'drill-error.log'), ($_.Exception.GetType().Name + ': ' + $_.Exception.Message) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    throw
} finally {
    if ($output) { $output.Dispose() }
    if ($responseStream) { $responseStream.Dispose() }
    if ($response) { $response.Dispose() }
    if ($pgStopped) {
        try { $null = Start-Database 'outage-drill cleanup recovery' }
        catch { [IO.File]::WriteAllText((Join-Path $attemptDirectory 'cleanup-recovery-failed.log'), 'PostgreSQL cleanup recovery failed.' + [Environment]::NewLine, [Text.UTF8Encoding]::new($false)) }
    }
}
