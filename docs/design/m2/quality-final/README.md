# Actual Resonance M2 — final captured surfaces

2026-10-03. All images are real Chrome app pixels with service workers blocked
to verify the current candidate, using the real rooted fixture catalog and real
receipt-protected collection operations. No study or mocked response substitutes
for these captures. Fixture names and sparse history are not invented user data.
`capture-index.json` records original local sources. Original attempts remain.

## Listener journey and findings

1. [Home, desktop](home-1440x900.png) / [phone](home-390x844.png).
   A lone history card stays bounded; albums/actual favorites carry the page.
   Sparse real history remains sparse rather than becoming recommendations.
2. [Library, desktop](albums-1440x900.png) / [phone](albums-390x844.png).
   Real credits/year, quiet missing-art initials and visible collection links.
   [Tracks](tracks-1440x900.png) separates desktop metadata without changing
   phone rows or context/action names.
3. [Search, desktop](search-1440x900.png) / [phone](search-390x844.png).
   Grouped context, one clear query field and actions from Track rows.
   [Same-name ambiguity](ambiguous-search-390.png) retains artist/album context.
4. [Album](album-detail-390.png) / [Artist](artist-detail-390.png).
   Focused metadata and collection actions, no unrelated catalog tabs.
   [Actual 10k catalogue return](catalog-return-1440.png) restores page 2 and
   exact original album ID/focus, not merely the same displayed name. The
   accent ring is an intentional keyboard-focus state.
5. [Empty Up Next](01-empty-390.png).
   One meaningful Find a song path; no empty Previous/Next/clear controls.
6. [Playing and Up Next, phone](02-playing-up-next-390.png) /
   [desktop](02-playing-up-next-1440.png).
   Actual “long” plays from the queue; Browser Song was added with Play next.
   On phone the first upcoming song stays above the persistent dock.
7. [Full player, phone](03-player-queue-390.png) /
   [desktop](03-player-queue-1440.png).
   The same audible song and queue agree. The separately captured
   [solo player](player-390x844.png) explicitly uses Play this song only; its
   saved queue is a different intentional mode, not a capture mismatch.
8. [Queue actions](05-queue-actions-390.png).
   Contextual move/remove/play actions preserve the row and keyboard context.
9. [Playlists, phone](playlists-390x844.png) /
   [desktop](playlists-1440x900.png).
   Stable wave marks, actual names/counts/dates and contextual creation.
10. [Creation](create-playlist-390.png) / [detail with long Unicode name](playlist-detail-390.png)
    / [empty detail](empty-long-playlist-390.png) /
    [deleted playlist](playlist-unavailable-390.png).
    Names remain complete at compact type; song addition is direct, and meaningful
    input dialogs retain focus/error state. The long-name playlist was a
    disposable real test collection, not a fictional mockup.
11. [Favorites](favorites-390x844.png) / [History](history-390x844.png) /
    [actual empty History](history-empty-390.png).
    History remains honest about real listening; the unused 10k fixture's empty
    state leads to keyboard Search without exposing “qualifying” terminology.
12. [Host management](host-1440.png).
    Actual listener scope/address and readable server/watch state. This accepted
    viewport stops above private folder paths; it is not a replacement for host
    boundary, enrollment or filesystem verification tests.

## Responsive coverage and limits

[320px player](player-320x568.png), [390px](player-390x844.png),
[430px](player-430x932.png), [844×390 landscape library](albums-844x390.png),
[landscape player](player-844x390.png), [768×1024 tablet](albums-768x1024.png)
and 1440×900 desktop were captured. No page errors or horizontal overflow were
recorded in these inspected matrices. Menu, no-result, empty, long-name,
Unicode and missing-art states have actual captures.

The screenshots do not prove physical Android ergonomics, soft-keyboard
occlusion, TalkBack, real text scaling or lock-screen audio. Those were deferred
by the owner. Snapshot compression is not a product defect. Earlier transitional
captures with ghosted dialog contents were rejected, and settled captures were
saved without overwriting their history.

[Final live keyboard return](manual-keyboard-artist.png) records the real Artist → Album → Back to artist state; its heading receives keyboard focus. Earlier Search ArrowDown/menu/Escape, creation-field focus and cancellation were manually observed as well. These checks are not a TalkBack session.

Generated [quality studies](../quality-studies/README.md) informed composition
and interaction choices. [Strict scores and remaining gates](../quality-review.md)
judge this running implementation. The all-category 9/10 target remains unmet.

## Playback-mode supplement — 2026-10-03

[Desktop](player-modes-1440.png) and [390px phone](player-modes-390.png) show
the real current shuffle/repeat controls, acknowledged shuffle order and paused
audio in the disposable rooted test catalog. These supersede the earlier player
control arrangement. The captures contain generated fallback art, not the owner's
private music artwork. [Correction and mode verification](../../../benchmarks/M2-folder-modes-verification.json)
records the final 70/70 browser campaign and remaining physical-device gates.
