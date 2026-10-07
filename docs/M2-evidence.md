# M2 evidence — owner-approved functional closure, 2026-10-07

The owner confirms successful different-folder, complete Android workflow and
background/recovery tests. Mark these **PASS — owner-reported**. Accessibility
and device performance verification/refinement are **ON HOLD — owner-deferred**,
retained explicitly in the [last strict review](design/m2/quality-review.md).
M2 was committed and pushed under the owner's renewed authorization. This is
functional release acceptance with declared exceptions, not a fresh all-category
9/10 score or proof that the deferred checks passed. M3 has not started.

Implementation and automated verification: 105 Chrome, 143 unit, 307 PostgreSQL
and 14 frontend-state passes; original suite skips remain documented. Both named
Windows symlink checks separately executed and passed without skips. Version-9
activation preserved existing owned data and the migration ledger. Detailed
results follow and in the [collection verification](benchmarks/M2-collections-verification.json).

## Historical candidate evidence — 2026-10-03

This publishes the product candidate and verified corrections. It does **not**
close M2 or claim every category reaches 9/10. The [strict review](design/m2/quality-review.md)
remains 8.8 overall with physical-device/accessibility/PWA/performance-confidence
gaps. Initial Android Chrome checks were deferred. On 2026-10-04 the owner
reported successful real-phone use and supplied a Tracks-screen photo. This
establishes basic same-LAN device use; the complete lifecycle/accessibility
matrix remains unperformed.

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

## Control consistency corrections — 2026-10-04

The [screenshot-led review](design/m2/control-consistency-review.md) fixes the
owner-reported native dropdown, decorative speaker and misplaced favorite.
Prepared songs now support favorite/album/playlist actions before playback;
metadata fencing, menu readiness, transport fit and rotation motion are covered
by new regressions. [Public captures](design/m2/control-consistency/README.md)
use the separate fixture library, excluding owner music and artwork.

[Verification](benchmarks/M2-controls-verification.json): 93/93 real browser
cases, including 14 new control checks; 11/11 frontend-state checks; 126 Go unit
and 270 PostgreSQL integration passes with existing skips. Failed attempts,
including the stopped PostgreSQL prerequisite after a pause, remain recorded.
Player gain is explicitly separate from device master volume: portable hardware
volume synchronization is unavailable. Physical Android verification remains
deferred at that run, and the pass did not close the full M2 quality gates.

## Native host folder picker and real-phone baseline — 2026-10-04

Choose folder now opens Windows’ native folder dialog and fills the Host form.
Suggested names follow folder changes until edited; native Cancel preserves
the existing choice. Enrollment and scanning remain separate explicit actions.
[ADR-011](adr/ADR-011-m2-host-native-folder-picker.md) records the host-only,
bounded helper lifecycle and platform boundary. The [form capture](design/m2/host-folder-picker.jpg)
contains no private paths. Actual native selection and cancellation were confirmed
by the owner and inspected in the running Host UI; the initial ambiguous Cancel
report was repeated rather than counted as a pass.

[Verification](benchmarks/M2-folder-picker-verification.json): 97/97 complete
browser cases, 4 new picker contract cases, 7 final host/accessibility checks,
11 frontend-state checks, 143 Go unit and 287 integration passes with existing
skips. Browser-controlled native-result cases cover reproducible failure/race
states, while a real Windows COM check and owner interaction establish native
dialog behavior. Eight final browser checks also passed after the strict-input
and parent-liveness guards. Root canonicalization, identity and overlap handling remain.

The owner reports that the listener works on the phone and supplied a physical
Tracks-screen photo. Record basic same-LAN Android use as confirmed. This does
not establish installation/update, background/lock-screen, physical rotation,
large-text, TalkBack, hardware-volume or the full inherited phone journey gates.
Private host paths, music, phone photos and raw evidence remain local.

## Whole collections, bulk editing and playlist views — 2026-10-07

The owner requested whole-library continuation first, then atomic bulk queue/
playlist organization, then in-playlist search/sort/density. The candidate now
uses complete durable source references with a bounded rolling queue, including
full-source shuffle/cycles and duplicate playlist occurrence selection. Play
starts the chosen Library, artist/album, playlist or Favorites source; current
selection authority and the persistent decoder stay protected. Explicit solo
play and history replay remain separate.

Accessible multi-select supports selected playback, Play next, group ordering,
copy/move/save and removal. Unknown/stale/capacity/faulted transfers save neither
side partially. Playlist search spans all entries; six view orders and two row
densities preserve the saved sequence. The selection entry point moved into the
header after real phone-size checks exposed player-dock obstruction.

[Verification](benchmarks/M2-collections-verification.json) records 105 real
Chrome checks across the existing and new collection campaigns, frontend checks,
serial PostgreSQL checks, preserved failures, backup/restore and measurements.
The 10,000-reference source-start/storage p95 is 1,144.084ms across 20 warm local
samples, including a queue read; initially only 128 queue occurrences are loaded.
This is projection work, not physical-device first-audio or LAN performance.
[ADR-012](adr/ADR-012-m2-collection-playback-and-bulk-editing.md),
[API contracts](api/M2-product.md) and [actual app captures](design/m2/collection-quality/README.md)
describe the source and interaction boundaries.

The two inherited Windows checks ran with Administrator permission on October 5:
`TestReviewMediaSymlinkCannotEscape` and
`TestWindowsKnownLocationReplacedBySymlinkIsDirectlyUnavailable` both executed
and passed without skips. Their raw hashes and explicit named results are in the
verification file. The ordinary unprivileged suites retain their listed skips;
the privileged result is not inferred from a package-level PASS.

An actual version-8 owner backup restored into a fresh database with exact data/
legacy-ledger equality. Its version-9 upgrade preserved 18 existing tables,
excluding only the grouping projection's refreshed completion timestamp from
the upgrade comparison. A late migration failure rolls back DDL and ledger;
schema readiness rejects a missing new index.

Automatic approval review initially rejected the live migration/restart. The
owner then explicitly approved it. The main library was upgraded to version 9,
with existing source/user rows and the legacy ledger preserved, and both listeners
became ready. Read-only verification checked the real 10-song catalog/UI and all
10 indexed Range responses without changing the owner's queue. Personal music,
covers, host paths and that private main-library capture are not published.
This pass does not close the full M2 quality target or the advanced Android,
TalkBack, zoom, installation/update and locked-screen/background audio gates.

## Parked after release — 2026-10-07

The owner requested idle disk cleanup before returning for M3. A fresh current
backup restored exactly, compact evidence was verified, and only checked
workspace-local rebuildable/archived files were removed. The offline recovery
kit subsequently restored the library and passed readiness/read/Range checks.
Both app and PostgreSQL are now stopped; no M3 implementation was started.
See [park/resume instructions](runbooks/M2-park-and-resume.md) and
[aggregate storage/recovery verification](benchmarks/M2-park-verification.json).
