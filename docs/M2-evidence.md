# M2 candidate evidence — 2026-10-03

This publishes the product candidate and verified corrections. It does **not**
close M2 or claim every category reaches 9/10. The [strict review](design/m2/quality-review.md)
remains 8.8 overall with physical-device/accessibility/PWA/performance-confidence
gaps. Android Chrome checks were deferred by the owner.

## Product and corrections

- Responsive Home, Tracks/Artists/Albums, literal grouped Search, Favorites,
  History, playlist overview/detail and Up Next; one persistent audio element.
- Ordinary Play follows the durable queue. Explicit solo playback stays separate.
  Existing occurrence selection and atomic collection insertion preserve receipts,
  revisions, natural-ended reports and decoder selection authority.
- Disabled folders leave Library/Search and group discovery; missing music in an
  enabled folder remains unavailable for recovery. Alternate enabled copies keep
  the Track visible. Disabling does not delete listener-owned state.
- Home presents playable recent/favorite previews. Older saved references remain
  in History/Favorites/playlists. Local test-only queue entries were removed.
- Host enrollment/verification release all newly rendered controls; status scans
  report unchanged/skipped files and the MP3/FLAC/WAV support boundary.
- Original embedded PNG/JPEG covers win over fallbacks. Decoder-verified MIME
  handles valid mislabeled covers without changing bytes or weakening bounds/hash checks.
- Shuffle changes the visible server queue order; current/history stay fixed and
  duplicate occurrences retain identity. Repeat off/all/one handles real natural
  endings. Manual Next still skips under repeat-one; loops issue fresh authority.
  Browser preferences survive reload without autoplay.
- Host operations remain on a separate loopback listener, with peer/socket/Origin/
  Host/Fetch Metadata checks. Public routes never expose host filesystem paths.

See [API behavior](api/M2-product.md), [ADR-009](adr/ADR-009-m2-product-boundaries.md),
[ADR-010](adr/ADR-010-m2-listening-and-collection-quality.md),
[component/state ownership](design/m2/quality-component-system.md),
[asset provenance/licenses](design/m2/assets.md) and [actual captures](design/m2/quality-final/README.md).

## Executed verification

| Check | Result |
| --- | --- |
| Go format and vet | PASS |
| Uncached Go unit checks | 126 pass, 0 fail, 1 privilege skip |
| Serial real-PostgreSQL integration checks | 268 pass, 0 fail, 11 explicit skips |
| Final complete real-Chrome campaign | 70/70 pass, no skipped/flaky cases |
| Independent M0 Chrome regression | 3/3 pass |
| Frontend state/order unit tests | 9/9 pass |
| Active frontend/tool formatting and whitespace checks | PASS |
| Owner collection, all supported songs | 10/10 decode/start, pause, seek, resume and navigation samples |
| Owner embedded covers | 9/9 served; one genuinely absent cover uses fallback |

`go test -count=1 -json ./...` and
`go test -count=1 -p 1 -parallel 1 -timeout 10m -tags integration -json ./...`
ran against a dedicated test database. Browser checks use a separate disposable
real catalog and host listener; they cannot repopulate the owner's library.
Axe and keyboard checks are included. Reproduce browser suites with
RESONANCE_M14_E2E/M15_E2E/M17_E2E/M2_E2E=1 and explicit
RESONANCE_E2E_BASE_URL/RESONANCE_ADMIN_E2E_BASE_URL pointed at disposable servers.

The [sanitized verification](benchmarks/M2-folder-modes-verification.json) records
counts, skipped test names, measured samples, limits and hashes of raw attempts.
Failed/interrupted attempts remain local. The successful PWA rerun observes
activation after all old clients close; it does not force activation over audio.

## Performance and remaining gates

The current 10,000-Track run uses five fresh Chrome contexts per width/profile,
50-row pages, blocked workers and browser cache disabled on a warm local database
/filesystem. Normal readiness p95 was 768.45ms at 390px and 544.89ms at 1440px.
CPU4x/80ms-network readiness p95 was 2083.26ms and 1490.04ms. Normal search p95
was 400.09ms/441.82ms including debounce. Initial normal samples overlap the
tail of the regression campaign. These are finite loopback observations, not
physical-device results or production SLOs.

Unperformed: physical Android touch/soft keyboard/orientation/large text/TalkBack,
installation/background/lock-screen behavior and two inherited Windows
file-symlink privilege cases. Other skips are explicitly opt-in historical
benchmarks/process campaigns. LAN HTTP secure-context restrictions remain
platform limits; publication does not bypass them or begin a later milestone.

Source publication excludes personal music, private paths/credentials, runtime
databases, executables, raw logs, old captures and duplicate image masters.
Current original app assets, their redistribution licenses, bounded reproduction
tools and sanitized evidence are included.

## Owner-reported Library navigation correction

The earlier candidate omitted a core journey: selecting a Library row queued
only that song, so Next could stop at its boundary and show a false queue-change
state. The owner requested continuation through the visible Library songs.
This is corrected using a bounded playback context, an atomic chosen-index
replacement and shared transport boundary/single-flight checks.

The [new verification](benchmarks/M2-library-transport-verification.json) records
the original two reproduced failures, final **73/73** real browser pass,
**126** Go unit / **269** PostgreSQL integration passes with inherited skips,
**10/10** frontend-state passes and **18/18** forward/backward transitions through
the owner's ten supported songs. Private song names/artwork are not exported.
Starting another Library page is an explicit new context; browsing remains
bounded, and stale decoders still cannot borrow a newer selection token.
The physical-device/quality gates remain open.

## Continuous personal-queue listening — 2026-10-04

Keep playing defaults on. The existing queue cycles in order; shuffle creates a
fresh bounded round at forward boundaries. Repeat-one and explicit opt-out are
retained. Last-song preview and handoff feedback explain the behavior without
recommended or fictional music. The [feature-scope research](design/m2/spotify-feature-fit.md)
distinguishes implemented M2, additional M2 priorities and later milestones.

[Verification](benchmarks/M2-continuous-verification.json): 79/79 real browser
cases, 11/11 frontend-state checks, 126 Go unit / 270 PostgreSQL integration
passes with inherited skips. Late failure injection proves cycle order/report
rollback; receipt replay and old-token rejection preserve authority. Current
page-only Library context, cross-round audible-history semantics, audio DSP and
physical Android limits remain explicit; M2 quality acceptance is not closed.
