# M2 component and state ownership — 2026-10-03

This supersedes the earlier layered refinement description. The loaded shell
uses components.css, player.css and icons.css. Former library/refinement/product
styles and obsolete asset variants are archived locally rather than shipped.

| Concept | Reusable implementation |
| --- | --- |
| Page and section hierarchy | page-heading, heading-copy, section-heading; long metadata uses a smaller full wrapping heading |
| Catalog/collection navigation | primary four-destination navigation, visible collection-links, three catalog tabs; detail contexts hide unrelated tabs |
| Track/result/occurrence | track-row, track-copy, item-title, item-subtitle, item-actions; contextual action names and occurrence IDs |
| Album/artist/collection cards | cover-card, artwork-frame, cover-credit; stable group credits/counts in the read projection |
| Playlist identity | collectionMark in ui.js: four quiet vector wave paths and the listener's name initial; no fabricated album image |
| Buttons and state | primary, quiet, danger, icon-only; 44px targets, visible focus, busy/disabled feedback |
| Frequent actions | context-menu: anchor placement, keyboard traversal, outside dismissal, generation-aware completion and focus return |
| Input and destructive dialogs | shared native dialog hierarchy; creation, rename, searchable song/destination pickers and explicit whole-playlist deletion |
| Listening/source state | listening-card and saved-queue driven by the pure observed-item/token projection |
| Empty and loading states | empty-state, empty-shelf, skeleton, notice; actual sparse/zero/unavailable content |
| Persistent and full player | player.css; one audio element, mirrored controls, bounded queue rail and shared artwork transition |
| Phone/landscape | 64px phone player, 56px four-destination navigation, safe areas; compact landscape rail/toolbar and two-column catalog |

Tokens: paper #f7f4ee, surface #fffdf8, ink #272823, muted #62655d,
accent #923e2c, line #dedad1, tint #eee6dd. Inter 400/500/600 and
Cormorant 600 are self-hosted. Operational text is 13–14px. Long detail names
remain complete and wrap; normal rows and card titles bound their density.

| Owner | Boundary |
| --- | --- |
| api.js | HTTP, typed errors, exact pending receipt bodies and safe retry |
| listening-session.js | Playback/session clocks, qualifying reports, final report/advance and outage retry |
| listening-state.js | Pure audible/saved/finished/detached projection; no mutation authority |
| favorites-state.js | Coalesced bounded membership, acknowledged values and stale-read fencing |
| library.js | Private navigation/catalog/search rendering with cancellation and generation fencing |
| user-library.js | Private collection controller, serialized manual queue writes, playlist/queue actions and bounded pages |
| player.js | Persistent/full transport and observed queue rail; native audio remains authoritative |
| contextual/picker/motion modules | Reusable input and rendering interactions, with explicit callbacks |
| pwa.js / sw.js | Capability, connection and waiting-update states; versioned shell-only cache |

There is no framework migration. Controllers still own substantial rendering
code; further extraction should follow a concrete reuse/state requirement.
The older decoder never acquires a newer tab's token. A reload never autoplays.

Catalog navigation retains at most three page/cursor/scroll/focus descriptors,
scoped to order and availability filtering; catalogue rows are fetched again.
Album Back returns to its actual parent artist, search or catalogue context.
Normal Up Next begins at the saved selection and follows advancement. Explicit
earlier/later browsing and reorder operations pin the visible window; occurrence
IDs and authoritative ordering are unchanged. Windows remain bounded to 100.

Artwork metadata reads coalesce duplicate Track references and retain only
ID/artwork URL/album ID for at most 200 entries and 30 seconds, cleared on
navigation. Prepared-player cover reads coalesce separately for one selection's
metadata, with generation checks. No queue tokens, audio, HTTP responses or
offline catalogue data enter those caches. Actual duplicate-occurrence checks
cover 201 references, earlier-page browsing and bounded metadata requests.

Destination headings receive appropriate focus; a returned catalogue card
regains its original logical-ID focus. Removing an occurrence focuses its
nearest remaining neighbour or the empty destination without taking focus from
a newer menu/view. Heading focus is visually indicated for keyboard input;
ordinary page loads do not look like a focused form field.

Motion communicates a cover expanding into its player, an acknowledged favorite
or button action, and occurrence reordering. An isolated transform-only artwork
layer uses a 280ms Web Animation, instead of a full-page document snapshot;
reorder is 160ms and input dialogs 220ms. Keyboard and reduced-motion paths
bypass entrance/reorder motion. No animation or new image blocks interaction.

Playback modes are owned by playback-modes.js (browser preferences and pure
order helpers). The player only presents state and delegates queue changes.
Queue advance owns repeat selection and atomic completion in storage. The
listening-session controller captures repeat intent in its persisted retry
request; a later preference change cannot rewrite a pending ended operation.
