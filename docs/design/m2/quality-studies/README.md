# Quality escalation studies

Generated 2026-10-01 against actual strict-audit Queue and phone Library captures.
These are exploration boards, not screenshots of implemented behavior. The raw
actual captures and all rejected/intermediate attempts remain under the local
quality evidence directory. The final report links the real implementation.

| Study | Adopted | Rejected or adapted |
| --- | --- | --- |
| [Queue / Library](01-queue-library.png) | Distinct audible song and saved-queue presentation, explicit resume, visible phone collection links, four primary destinations | Fictional tracks/durations, duplicate transport in every card and drag-only reordering; actual controls preserve observed selection authority |
| [Search / Playlist](02-search-playlist.png) | Artwork/title/artist/album context, grouped Search, playlist-specific detail and nearby song actions | Additional category tabs in Search, invented commercial artwork/catalog and decorative result durations; M2 search remains literal and bounded |
| [Collection / Queue](03-collection-queue.png) | Playlist cards, a deliberate New playlist action, contextual move/remove controls, coherent queue/player relationship | Fake collage covers and invented song counts; actual playlist marks derive a quiet wave rhythm from the stable collection ID, with the listener's own name/initial/count |

The implementation retains the approved paper/rust library and dark focused
player. It uses real artwork when present, title initials with quiet fallback
photographs for missing album art, and native vector signal marks for playlists.
It does not import the boards' fictional data or navigation inconsistencies.

Source and actual implementation were inspected together. Improvements were
judged on the running product: Search ambiguity, narrow navigation, saved queue
versus audible playback, playlist creation/detail, and actual row actions.
