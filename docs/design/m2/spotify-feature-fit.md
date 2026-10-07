# Spotify feature reference and Resonance M2 fit

Reviewed 2026-10-04 against official Spotify support/newsroom pages, the current
Resonance implementation, CURRENT_MILESTONE.md and ROADMAP.md. Spotify is a
quality reference; its catalog, branding and product model are not copied.

## The reported end-of-queue behavior

Spotify's [Autoplay](https://support.spotify.com/us/article/autoplay/) adds similar
music after an album, playlist or selection ends. That is recommendation behavior.
Resonance M2 explicitly excludes recommendations; the roadmap places them in M7.

The M2 alternative is continuous playback of the existing personal queue:
Keep playing defaults on, ordered rounds wrap, and shuffled rounds are rebuilt
atomically at their boundary. Queue occurrence count stays bounded; this is not
an infinitely growing list. Repeat-one takes precedence at natural endings.
Turning Keep playing off with Repeat off permits an intentional finite end.
Explicit solo-song playback stays separate from the saved queue.

## Feature map

| Feature/reference | Current Resonance status | M2 decision |
| --- | --- | --- |
| Continuous listening, normal shuffle and repeat ([Autoplay](https://support.spotify.com/us/article/autoplay/), [Shuffle](https://support.spotify.com/us/article/shuffle-play/)) | Local continuous cycles and fresh boundary shuffle added in this pass; no recommended tracks | Implemented inside M2. Next round is explained; stop behavior stays available |
| Clear queue order, Play next, remove/reorder ([Play Queue](https://support.spotify.com/us/article/play-queue/)) | Real durable queue, contextual actions, duplicate occurrences, token/revision fencing | Existing M2. Continue refining feedback and phone ergonomics |
| Whole-library/whole-collection listening | Full-source snapshots and rolling queue continuation now cover Library, artist/album, playlist and Favorites Play | Implemented and activated on the main library after explicit approval on 2026-10-07. See ADR-012 and collection verification |
| One-tap reshuffle ([2026 control update](https://newsroom.spotify.com/2026-05-28/playlist-folders-mobile-queue-controls-updates/)) | Shuffle toggle and fresh round shuffle exist; no separate reshuffle action | P1 M2: reshuffle only upcoming occurrences through existing revision/receipt semantics, preserving the playing song |
| Sleep timer ([Now Playing controls](https://newsroom.spotify.com/2025-05-07/experience-a-new-dimension-of-music-discovery-with-more-controls-and-enhanced-tools/)) | Missing | P1 M2: timed pause and end-of-current-song option with explicit cancellation. Use absolute deadlines and verify Android background behavior; browser timers are not native-service guarantees |
| Bulk playlist/queue editing ([2026 control update](https://newsroom.spotify.com/2026-05-28/playlist-folders-mobile-queue-controls-updates/)) | Accessible selection and atomic removal, group ordering, copy/move/save now exist | Implemented in the 2026-10-07 candidate with occurrence IDs, revision/receipt checks, capacity handling and rollback of both sides |
| Playlist/library organization ([Your Library](https://support.spotify.com/us/article/your-library/), [playlist folders](https://newsroom.spotify.com/2026-05-28/playlist-folders-mobile-queue-controls-updates/)) | Playlist CRUD plus all-entry literal search, original/title/artist/album/recent view orders and compact/comfortable rows | Search/sort/density implemented in this candidate; view choices preserve the saved sequence. Playlist folder grouping remains optional M2 work |
| Faster keyboard workflows ([shortcuts](https://support.spotify.com/us/article/keyboard-shortcuts/)) | Search shortcut, native focus/menu controls and capability-dependent Media Session handlers | P1 M2: documented play/pause/seek/queue/shuffle shortcuts, respecting inputs, dialogs and focus; no global keyboard interception |
| Saved volume/mute and player preferences | Shuffle/repeat/continuous and app volume/mute preferences persist; player gain is explicitly separate from device master volume | Already implemented and browser-verified in the controls pass; portable hardware-volume mirroring is unavailable |
| Local playlist covers/embedded lyrics | Missing | P2 M2 candidates: user-owned, bounded image/tag data only, with storage/decoding/privacy limits. No automatic commercial lyrics service or invented lyrics |
| Crossfade, gapless, Automix ([track transitions](https://support.spotify.com/us/article/tracks-transitions/)) | Not implemented; one persistent audio element is the current contract | Conditional stretch, not a checkbox feature. Metadata preparation can reduce startup gaps in M2. True sample-accurate gapless/crossfade earns a separate decoder/session design and device evidence; do not regress history/streaming to claim parity |
| Loudness normalization ([normalization](https://support.spotify.com/us/article/volume-normalization/)) | Missing | M4: the roadmap explicitly assigns loudness/normalization strategy to the media pipeline. Do not guess gain from file size or current volume |
| Smart Shuffle/Radio/personalized discovery ([Shuffle](https://support.spotify.com/us/article/shuffle-play/)) | Deliberately absent | M7 recommendation work; simple random cycling of owned queue items is allowed in M2 and is not Smart Shuffle |
| Spotify Connect-style pairing/remote control, accounts, collaborative sharing | Absent | M3 or later: identity, sessions, pairing, revocation and network trust boundaries must exist first |
| Downloaded offline music | Deliberately absent; shell-only PWA cache exists | Outside the authorized M2 scope. Keep media/Range/data out of Cache Storage |

These are implementation priorities and scope judgments, not claims that the
missing features already exist. A feature being feasible inside M2 does not mean
it is required to clone every Spotify setting. The next highest-value pass is
the remaining physical-device/lifecycle verification and independent review of
the new collection candidate; optional sleep timer and playlist folders remain
separate candidates. Extra artwork does not close those gates.

## Acceptance remains open

Physical Android Chrome touch, keyboard, rotation, large text, TalkBack,
installation and locked-screen/background audio were deferred by the owner.
The prior all-category 9/10 quality target remains unmet. More product features
do not replace those tests or make this a production-grade claim.
