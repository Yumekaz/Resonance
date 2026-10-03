# Project Resonance — personal music library

M2 provides a responsive personal-library listener, literal local search, persistent compact/full player, shell-only PWA support and separate host administration. See the [current product review](docs/design/m2/quality-review.md), [actual app captures](docs/design/m2/quality-final/README.md), [verification and remaining gates](docs/M2-evidence.md), [API contract](docs/api/M2-product.md), and [architecture decisions](docs/adr/ADR-009-m2-product-boundaries.md). Physical Android and inherited Windows symlink gates remain open; publication is not an M2 completion claim.

The warm library and dark full player use original Resonance artwork fallbacks, self-hosted fonts and icons. See [component and state ownership](docs/design/m2/quality-component-system.md) and [asset provenance and licenses](docs/design/m2/assets.md). Embedded cover art takes priority. Playback supports queue progression, Previous/Next, shuffle and repeat-one/repeat-all. Preferences survive reload without autoplay; Up Next always shows the actual server queue order.

With a configured database, open the music listener on its configured address. On the **server computer**, open `http://127.0.0.1:8081/` to add music folders, verify their identity, enable/disable them and scan. Use `-admin-addr 127.0.0.1:<port>` to change the admin port, or `-admin-addr ""` to disable it. The admin port is loopback-only and must never be reverse-proxied, tunneled or forwarded. The listener has no filesystem-management routes.

Selecting a Library song starts the playable songs on the currently displayed page at that position, so Previous/Next follow the visible music. Starting a new context replaces Up Next atomically; explicit Play now/Play next/Add to queue menu actions retain their individual-song semantics.

The listener combines **all enabled music folders**. Adding another folder does not replace the existing selection; disable folders you no longer want in Library or Search. Disabling never deletes music or saved playlist/favorite/history references. Missing files within an enabled folder remain visible as unavailable. The current importer supports MP3, FLAC and WAV; other formats are skipped and reported by Scan now. Embedded PNG/JPEG covers take priority over generated fallbacks, including valid images whose tag writers used the wrong PNG/JPEG MIME label.

No frontend build or Node process is required to run the shipped server. Browser assets remain embedded in Go. Node, Playwright, axe-core and Prettier are development/test tools only. Search is a bounded literal metadata query over the existing PostgreSQL schema; no migration or external search service is added.

On localhost, capable browsers can install the listener and cache its static shell. Trusted LAN HTTP may not permit installation or service workers; ordinary browser playback remains available. Music and writes are never cached for offline use. A new shell waits for existing app tabs to close instead of interrupting playback. M3 owns HTTPS and remote access.

Project Resonance is a single Go server with PostgreSQL-backed catalog and personal-library state. It streams an operator-configured WAV file and enrolled local media to a browser player. M0 is the unauthenticated local/LAN diagnostic path; no Internet access feature is included.

M1.1 adds PostgreSQL metadata foundations, M1.2 adds host-only folder enrollment, M1.3A adds authoritative foreground reconciliation, M1.4 adds catalog browsing and indexed playback, M1.5 adds durable queue/playlists/favorites/history, and M1.6 adds best-effort filesystem hints with recovery through the same authoritative scanner. M1.7 records single-node bootstrap, recovery, restore, scale and regression evidence; it adds no production endpoint or service. With `RESONANCE_DATABASE_URL`, `/` opens the library and `/demo` retains the M0 diagnostic player. Without a database, the M0 demo remains at `/` and `/ready` reports the catalog as unconfigured. Start with the [M1.7 clean bootstrap runbook](docs/runbooks/M1-7-bootstrap.md) and [complete backup/restore runbook](docs/runbooks/M1-7-backup-restore.md). Read the [operational model and limitations](docs/M1-7-operations.md) and the [dated M1.7 evidence and gate report](docs/M1-7-evidence.md). See also the [PostgreSQL runbook](docs/runbooks/M1-1-postgres.md), [library runbook](docs/runbooks/M1-2-library.md), [catalog contract](docs/api/M1-4-catalog.md), [M1.5 user-library contract](docs/api/M1-5-user-library.md), and [M1.6 status and recovery contract](docs/api/M1-6-library-status.md).

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
- `GET /api/v1/library/status`: path-free watcher, root-verification, dirty-work, scan, and recovery diagnostics. Watcher failure alone does not change readiness.
- `GET /api/v1/demo-track`: logical ID, title, and stream URL.
- `GET /media/demo-track`: full `200`, or single bounded/open-ended/suffix Range `206` with exact length and inclusive `Content-Range`.
- `GET /api/v1/tracks`, `/artists`, `/albums` and detail/navigation routes: paginated local catalog browsing. `GET` and `HEAD /api/v1/tracks/{id}/stream` resolve an indexed Track under enrolled roots and use the same byte-serving function as `/media/demo-track`. See the [M1.4 contract](docs/api/M1-4-catalog.md).
- `/api/v1/queue`, `/playlists`, `/favorites`, `/listening-sessions`, and `/history`: durable Track-based user-library state. Natural audio end reports completion and advances the queue in one transaction. The database stores selection, while browser play/pause/buffering/position remain client-owned. See the [M1.5 contract](docs/api/M1-5-user-library.md).
- `go run . library verify <root-id>`: explicit host-side identity verification or rebind for an enrolled root. Migration 0008 marks existing roots unverified; scans cannot reconcile absence until host verification succeeds.
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

The integration command requires the dedicated `RESONANCE_TEST_DATABASE_URL` described in the PostgreSQL runbook. To enroll and scan a host directory, apply migrations and use `go run . library add -path <directory> -name <name>`, followed by `go run . library scan <root-id>`. The listener cannot enroll paths. Host-local administration can enroll an explicitly entered server folder through its separate loopback-only listener.

Windows tests create a junction through a fixed PowerShell command to verify containment. A separate file-symlink test explicitly skips if Windows denies symlink creation. The application itself launches no child processes. `go test -race ./...` additionally requires a working CGO/C compiler toolchain; it is not silently treated as passed when unavailable.

## Benchmark

```powershell
go run ./cmd/bench -url http://127.0.0.1:8080/media/demo-track -count 100 -out docs/benchmarks/M0-review-raw.json
```

The harness measures one full response, one first range, and 100 seeded random ranges. It validates status, Content-Range, Content-Length, and consumed byte counts. Invalid responses fail the run. Median averages the middle pair; p95 uses nearest rank. HTTP timings are separate from the browser's seek-to-playing event measurement. See [the benchmark report](docs/benchmarks/M0.md) for actual results and limitations.

For indexed media, point the same harness at `/api/v1/tracks/{id}/stream`; `go run ./cmd/m14measure -track <track-id> -out docs/benchmarks/M1-4-queries.json` measures catalog pages, detail, and Track resolution with raw per-request timings. See [M1.4 benchmark evidence](docs/benchmarks/M1-4.md).

M1.5 queue, playlist, favorite, history, and read/Range overlap commands and raw results are in [M1-5.md](docs/benchmarks/M1-5.md). The benchmark uses a dedicated PostgreSQL test schema and makes no latency SLO claim.
