# Project Resonance — M0 stream and M1.1 persistence foundations

One Go server streams one configured WAV file to a browser player. M0 is a local/LAN experiment with no authentication or Internet access feature.

M1.1 adds PostgreSQL metadata foundations and a metadata parser evaluation. It does not enroll folders or scan a library. Without `RESONANCE_DATABASE_URL`, the accepted M0 demo continues to run; `/ready` reports the catalog as unconfigured. See the [M1.1 PostgreSQL runbook](docs/runbooks/M1-1-postgres.md) for setup, migrations, integration tests, backup, and restore.

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

Open <http://127.0.0.1:8080/>. The default listener is loopback. For a phone on a trusted LAN, explicitly bind to the laptop's private address using `-addr <laptop-LAN-IP>:8080`. Permit the port only on the private network in the host firewall. Anyone able to reach this unauthenticated HTTP listener can retrieve the configured file. Do not forward the port to the Internet.

`-media` selects a trusted PCM WAV without source edits. Its parent directory is the configured file root for this diagnostic track; the basename resolves through `os.OpenInRoot`. This is not M1 library-folder enrollment. Links escaping that root and Windows alternate data streams are rejected. The configured parent directory itself is trusted operator input; this is not a sandbox against a privileged local attacker, mount changes, or malicious hard links. The only public media ID is `demo-track`, never a client pathname. Web assets are embedded, so a built executable does not serve arbitrary files or links from the working directory.

## API and Range policy

- `GET /health`: status/version JSON (server liveness, not codec validation).
- `GET /ready`: PostgreSQL and migration readiness. Returns `503` when the catalog dependency is unconfigured or unavailable, and `200` when the configured database is reachable at the current schema version. It does not claim that scanning or browsing exists.
- `GET /api/v1/demo-track`: logical ID, title, and stream URL.
- `GET /media/demo-track`: full `200`, or single bounded/open-ended/suffix Range `206` with exact length and inclusive `Content-Range`.
- Unsatisfiable ranges return `416` with `Content-Range: bytes */size`. Malformed single-byte syntax returns `400`. Reversed intervals return `416`.
- Multiple ranges (including repeated Range fields) and unsupported units are ignored as a whole, returning full `200`; they are never partially interpreted.
- `HEAD` ignores Range and returns full representation headers without a body.
- No validators are advertised. A request containing `If-Range` falls back to full `200`; media uses `Cache-Control: no-store`.
- Missing/unreadable media returns a sanitized `503`. A mid-transfer read/write failure aborts the response. Corrupt audio is reported by the browser; the server does not decode or validate codecs.

The file must remain immutable during transfer. Truncation/read failure is detected, but a same-size in-place edit is not reliably detected. Streaming uses a 32 KiB buffer and checks cancellation between reads and writes. Write operations have a rolling 30-second deadline. Pausing playback does not necessarily cancel a browser download.

Every routed response has `X-Request-ID`. Media requests emit JSON logs with that ID, logical media ID, Range presence/raw value/application, selected inclusive offsets, HTTP status, intended media bytes, media bytes accepted by writes, duration, and cancellation/error state. Error-document bytes are excluded from media-byte counters. HEAD has zero served bytes. Written bytes do not prove delivery or playback; selected offsets are not the original requested bounds. The UI checks reachability on initial load.

## Test

```powershell
go fmt ./...
go vet ./...
go test -count=1 ./...
go run ./cmd/fixture -out data/demo.wav -seconds 300
npm ci
npm run test:e2e
```

Keep a freshly built server running on `127.0.0.1:8080` for E2E. Tests require the generated 300-second fixture. Browser tests disable cache and throttle downloads to 256 KiB/s, verify playback before full transfer, seek to an unbuffered position, validate a new matching partial response, and require playback to advance after seeking. They also test unreachable-server and corrupt-media UI states.

Windows tests create a junction through a fixed PowerShell command to verify containment. A separate file-symlink test explicitly skips if Windows denies symlink creation. The application itself launches no child processes. `go test -race ./...` additionally requires a working CGO/C compiler toolchain; it is not silently treated as passed when unavailable.

## Benchmark

```powershell
go run ./cmd/bench -url http://127.0.0.1:8080/media/demo-track -count 100 -out docs/benchmarks/M0-review-raw.json
```

The harness measures one full response, one first range, and 100 seeded random ranges. It validates status, Content-Range, Content-Length, and consumed byte counts. Invalid responses fail the run. Median averages the middle pair; p95 uses nearest rank. HTTP timings are separate from the browser's seek-to-playing event measurement. See [the benchmark report](docs/benchmarks/M0.md) for actual results and limitations.
