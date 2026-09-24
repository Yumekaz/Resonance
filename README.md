# Project Resonance — M1.5 personal library

One Go server streams one configured WAV file to a browser player. M0 is a local/LAN experiment with no authentication or Internet access feature.

M1.1 adds PostgreSQL metadata foundations, M1.2 adds host-only folder enrollment, M1.3A adds authoritative foreground reconciliation, M1.4 adds catalog browsing and indexed playback, and M1.5 adds a durable queue, playlists, Track favorites, and meaningful listening history. With `RESONANCE_DATABASE_URL`, `/` opens the library and `/demo` retains the M0 diagnostic player. Without a database, the accepted M0 demo remains at `/` and `/ready` reports the catalog as unconfigured. See the [PostgreSQL runbook](docs/runbooks/M1-1-postgres.md), [library runbook](docs/runbooks/M1-2-library.md), [catalog contract](docs/api/M1-4-catalog.md), and [M1.5 user-library contract](docs/api/M1-5-user-library.md).

## Requirements

- Go 1.25 or newer (`os.OpenInRoot` provides filesystem containment; the patched `pgx` version requires Go 1.25)
- A browser with PCM WAV playback support
- Node.js 18+, Playwright, and installed Chrome for browser tests only

## Start

From the repository root:

```powershell
go run ./cmd/fixture -out data/demo.wav -seconds 300
go run . -media data/demo.wav
```

Open <http://127.0.0.1:8080/>. The default listener is loopback. For a phone on a trusted LAN, explicitly bind to the laptop's private address using `-addr <laptop-LAN-IP>:8080`. Permit the port only on the private network in the host firewall. Anyone able to reach this unauthenticated HTTP listener can retrieve the configured demo file and enrolled library media. Do not forward the port to the Internet.

`-media` selects a trusted PCM WAV without source edits. Its parent directory is the configured file root for this diagnostic track; the basename resolves through `os.OpenInRoot`. This is not M1 library-folder enrollment. Links escaping that root and Windows alternate data streams are rejected. The configured parent directory itself is trusted operator input; this is not a sandbox against a privileged local attacker, mount changes, or malicious hard links. The only public media ID is `demo-track`, never a client pathname. Web assets are embedded, so a built executable does not serve arbitrary files or links from the working directory.

## API and Range policy

- `GET /health`: status/version JSON (server liveness, not codec validation).
- `GET /ready`: PostgreSQL, migration, and grouping-backfill readiness. Returns `503` when the catalog dependency is unconfigured, unavailable, incompatible, or incompletely backfilled.
- `GET /api/v1/demo-track`: logical ID, title, and stream URL.
- `GET /media/demo-track`: full `200`, or single bounded/open-ended/suffix Range `206` with exact length and inclusive `Content-Range`.
- `GET /api/v1/tracks`, `/artists`, `/albums` and detail/navigation routes: paginated local catalog browsing. `GET` and `HEAD /api/v1/tracks/{id}/stream` resolve an indexed Track under enrolled roots and use the same byte-serving function as `/media/demo-track`. See the [M1.4 contract](docs/api/M1-4-catalog.md).
- `/api/v1/queue`, `/playlists`, `/favorites`, `/listening-sessions`, and `/history`: durable Track-based user-library state. Natural audio end reports completion and advances the queue in one transaction. The database stores selection, while browser play/pause/buffering/position remain client-owned. See the [M1.5 contract](docs/api/M1-5-user-library.md).
- Unsatisfiable ranges return `416` with `Content-Range: bytes */size`. Malformed single-byte syntax returns `400`. Reversed intervals return `416`.
- Multiple ranges (including repeated Range fields) and unsupported units are ignored as a whole, returning full `200`; they are never partially interpreted.
- `HEAD` ignores Range and returns full representation headers without a body.
- No validators are advertised. A request containing `If-Range` falls back to full `200`; media uses `Cache-Control: no-store`.
- Missing/unreadable media returns a sanitized `503`. A mid-transfer read/write failure aborts the response. Corrupt audio is reported by the browser; the server does not decode or validate codecs.

The demo file is assumed immutable during transfer. Indexed playback verifies the enrolled root, checks the opened file against the scanner's size, modification-time, and available native evidence, and rechecks that the handle still occupies its confined path before headers. It continues from that open handle after catalog changes. A same-size, same-mtime in-place edit is not reliably detected without a fresh scan. Streaming uses a 32 KiB buffer and checks cancellation between reads and writes. Write operations have a rolling 30-second deadline. Pausing playback does not necessarily cancel a browser download.

Every routed response has `X-Request-ID`. Media requests emit JSON logs with that ID, logical media ID, Range presence/sanitized value/application, selected inclusive offsets, HTTP status, intended media bytes, media bytes accepted by writes, duration, and cancellation/error state. Error-document bytes are excluded from media-byte counters. HEAD has zero served bytes. Written bytes do not prove delivery or playback; selected offsets are not the original requested bounds. The UI checks reachability on initial load.

## Test

```powershell
go fmt ./...
go vet ./...
go test -count=1 ./...
go test -p 1 -tags=integration -count=1 ./...
go run ./cmd/fixture -out data/demo.wav -seconds 300
npm ci
npm run test:e2e
```

Keep a freshly built server running on `127.0.0.1:8080` for E2E. Tests require the generated 300-second fixture. Browser tests disable cache and throttle downloads to 256 KiB/s, verify playback before full transfer, seek to an unbuffered position, validate a new matching partial response, and require playback to advance after seeking. They also test unreachable-server and corrupt-media UI states.

The M1.4 browser test requires a migrated and scanned catalog server with a 300-second WAV titled `long` and a tagged MP3 with Artist `Browser Artist`, Album `Browser Album`, and title `Browser Song`. Set `RESONANCE_M14_E2E=1` and run `npx playwright test tests/e2e/library.spec.js`. It remains skipped during the unconfigured M0 demo run.

The M1.5 browser test uses that catalog plus a generated two-second `Short.wav`. Set `RESONANCE_M15_E2E=1` and run `npx playwright test tests/e2e/user-library.spec.js`. It covers queue placement and controls, stale-tab selection fencing, lost-response and version-conflict retries, natural-end history, playlist/favorite create/edit/reorder/delete, seek-to-end behavior, reload persistence, and decoder failure without fabricated history.

The integration command requires the dedicated `RESONANCE_TEST_DATABASE_URL` described in the PostgreSQL runbook. To enroll and scan a host directory, apply migrations and use `go run . library add -path <directory> -name <name>`, followed by `go run . library scan <root-id>`. The web client cannot enroll paths.

Windows tests create a junction through a fixed PowerShell command to verify containment. A separate file-symlink test explicitly skips if Windows denies symlink creation. The application itself launches no child processes. `go test -race ./...` additionally requires a working CGO/C compiler toolchain; it is not silently treated as passed when unavailable.

## Benchmark

```powershell
go run ./cmd/bench -url http://127.0.0.1:8080/media/demo-track -count 100 -out docs/benchmarks/M0-review-raw.json
```

The harness measures one full response, one first range, and 100 seeded random ranges. It validates status, Content-Range, Content-Length, and consumed byte counts. Invalid responses fail the run. Median averages the middle pair; p95 uses nearest rank. HTTP timings are separate from the browser's seek-to-playing event measurement. See [the benchmark report](docs/benchmarks/M0.md) for actual results and limitations.

For indexed media, point the same harness at `/api/v1/tracks/{id}/stream`; `go run ./cmd/m14measure -track <track-id> -out docs/benchmarks/M1-4-queries.json` measures catalog pages, detail, and Track resolution with raw per-request timings. See [M1.4 benchmark evidence](docs/benchmarks/M1-4.md).

M1.5 queue, playlist, favorite, history, and read/Range overlap commands and raw results are in [M1-5.md](docs/benchmarks/M1-5.md). The benchmark uses a dedicated PostgreSQL test schema and makes no latency SLO claim.
