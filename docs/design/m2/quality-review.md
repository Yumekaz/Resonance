# Resonance M2 quality escalation — strict review

## Owner closure decision — 2026-10-07

M2 is closed for the owner-approved functional scope and may be published. The
owner reports successful different-folder testing, complete Android workflow
testing, and background/recovery testing. These are **owner-reported passes**,
not newly agent-observed traces or a fresh numerical design audit.

| Acceptance item | Current disposition | Evidence |
| --- | --- | --- |
| Different-folder enrollment, discovery and playback | PASS — owner-confirmed | Direct owner confirmation on 2026-10-07 |
| Android listener workflows | PASS — owner-confirmed | Owner states complete Android testing is done |
| Background and recovery workflows | PASS — owner-confirmed | Owner states background/recovery testing is done |
| Two inherited Windows file-symlink cases | PASS — executed | Administrator run, 2026-10-05; named results in collection verification |
| Full accessibility verification | **ON HOLD — owner-deferred** | TalkBack, real text scaling/zoom and complete assistive journeys remain unverified |
| Device performance profiling and stress-tail refinement | **ON HOLD — owner-deferred** | Actual device/compositor and cold Wi-Fi evidence remain incomplete; recorded stress outliers remain |

The two on-hold items are retained for a later strict quality review, with no
claim that they passed. They do not block this owner-authorized M2 release.
The 2026-10-03 score table below is historical and is not automatically increased
by test completion or owner acceptance: the all-category 9/10 target has **not**
been freshly audited or established. Do not publish an invented 10/10 rating.

PWA shell/lifecycle boundaries remain verified as documented. Owner-reported
background/recovery passes do not establish every installation/update pathway.
LAN HTTP capability limits remain an accepted platform boundary; HTTPS/security
work stays in M3. No insecure-origin overrides or certificate bypasses are added.
M3 is **not started**; the project will be parked until the owner resumes it.

## Historical strict review — 2026-10-03

**Historical strict review — implementation and desktop verification complete;
quality acceptance remains open.** The empty-queue and phone queue-density
findings were fixed and reinspected. No 9 is inferred from a passing test count.

2026-10-03, Asia/Calcutta. This supersedes earlier claims of 10/10 and earlier
statements that no actionable product findings remained. The owner's full
strict 6.5/10 audit is the baseline. This review predates the owner-authorized source publication; the quality gates remain open.

**Overall: 8.8/10. The requested all-category 9/10 target is NOT MET.**

The implementation is ready for independent code and product review. It is
not ready for unconditional M2 acceptance. Physical Android Chrome verification
was explicitly deferred by the owner. Performance under emulated CPU stress
also retains a measurable weakness. These are not passing test results.

## Before / after

| Before | After | Why |
| --- | --- | --- |
| Ordinary Play could leave audible playback separate from an unrelated saved queue | Ordinary Play follows Up Next; explicit Play this song only retains the saved queue | Play next now targets the listener's ordinary listening sequence |
| Stopped here / ambiguous selection copy | Audible song, saved queue, automatic continuation and natural end/replay have distinct presentation | User concepts describe actual playback instead of internal tokens |
| Selecting a queued occurrence inserted another copy; collection writes could leave a prefix | Existing-occurrence selection and atomic bounded collection addition | Keeps intentional duplicates and makes whole-collection failure recoverable |
| Playlist detail retained catalog tabs; queue access differed by device | Four primary destinations and direct player-to-queue access; detail owns its context | Predictable navigation without hidden horizontal categories |
| Modal-heavy repeated song/reorder operations | Contextual menus and inline desktop queue controls, with keyboard traversal and focus return | Repeated tasks preserve context |
| Bare playlist forms and repeated detail names | Collection cards with original wave marks/counts, contextual creation, compact detail toolbar, in-place song search and deliberate whole-playlist deletion | Organization feels like a music collection and avoids accidental whole-sequence deletion |
| Search group rows lacked useful context | Artwork, title, artist, album/year/counts, grouped results, clear action and keyboard selection | Same-name tracks/albums are distinguishable |
| Small metadata, dominant headings and decorative captions | 13–14px operational text, smaller long-name headings, quieter missing-art cards and no repeated generated captions | Actual music and useful metadata take priority |
| Mobile scrolling tabs, clipped smallest player and weak landscape shell | Visible collection links, four-destination bottom navigation, 64px player and compact landscape rail/catalog | Navigation remains discoverable at 320, 390, 430, 844 landscape and tablet widths |
| Repeated generic More / favorite actions | Title/artist/album/occurrence-aware actions; native input dialogs and restored form/menu focus | Keyboard and assistive navigation have meaningful context |
| Layered CSS overrides and mixed global state | Loaded component/player styles, private controllers, extracted receipt/session/projection/favorite/menu/picker modules | State ownership and repeated components have explicit boundaries |
| Artwork/favorite/playlist metadata fan-out | Group read projections, visible bounded favorite lookup and on-demand paged playlist destination search | Large saved collections do not need full startup traversal |
| Update readiness shared an overwritten connectivity badge | Independent capability, connection and waiting-update states | Connectivity polling cannot erase a ready update; updates cannot interrupt audio |
| Full-page native snapshot artwork animation | Isolated transform-only artwork layer, with cached unchanged queue rail rendering | Preserves spatial continuity and improves normal frame pacing without blocking input/audio |
| Empty Queue/History provided little direction | Shared natural-language empty states with a direct Find a song path; meaningless empty queue controls are hidden | First use has a clear next action |
| Phone Now Playing card pushed the upcoming song beneath the dock | Compact current-song summary and keyboard-accessible player entry | The first upcoming song stays above the dock at 390×844 |
| Album Back lost its parent catalogue/artist position | Real parent breadcrumbs and three bounded catalogue page/scroll/focus descriptors | A return restores the exact original logical album ID, including among same-name albums |
| Large queues began among earlier songs | Current selection and upcoming items lead the bounded window; earlier browsing remains explicit | Up Next opens at the relevant part of the sequence |
| Repeated occurrences fetched the same artwork metadata repeatedly | Bounded coalescing and short-lived artwork-only projections; prepared cover reads coalesce separately | 201 duplicate references and earlier browsing remain within three metadata reads in the regression |
| Keyboard view entry/removal lost focus | Destination heading, original catalogue card and nearest remaining occurrence receive focus | Repeated organisation stays in context |

## Category scores

These are judgments of the actual M2 product, not a work-effort score or an
average of test results. There are no 10s. Deliberately deferred authentication,
remote access, recommendations, semantic search, transcoding and offline music
are not deductions.

| Category | Score | Strongest evidence | Remaining weakness | Reason for this score / what prevents higher |
| --- | --- | --- | --- | --- |
| Product design | 9 | Real catalog Play → Up Next, explicit solo mode, saved-queue resume and end/replay agree with the decoder; real playlist/song workflows | First use still requires a host operator to enroll music and configure LAN listening | The central listening model is coherent and end-to-end. A frictionless phone setup journey is not established |
| Information architecture | 9 | Four primary destinations, visible collection links, immediate player queue entry and context-specific detail navigation | Phone Library still contains several distinct collection destinations | Hierarchy is predictable and has no horizontal-discovery trap; further simplification needs real-user validation |
| UX | 9 | Search-to-play/save, atomic album/playlist queueing, existing-occurrence play, bounded pickers and contextual reorder/remove | Individual removal has no undo; stale multi-tab conflicts require refresh/retry | Frequent operations preserve context and errors retain intent. More recovery convenience would improve it |
| UI design | 9 | Shared buttons/rows/cards/forms, 44px controls, readable operational text and contextual state feedback in actual captures | Very long names and tiny heights necessarily require scrolling | Major screens have deliberate hierarchy and control treatment; extreme-content comfort has finite coverage |
| Visual design/polish | 9 | Consistent paper/rust library and focused dark player, quieter fallbacks, resolved landscape and long-name detail captures | Sparse history and finite fallback imagery still limit variation | The ordinary surfaces now share the player’s visual care. It remains a restrained library product, not an extraordinary art direction in every content state |
| Interaction design | 9 | Anchored keyboard menus, single-flight feedback, persistent audio, immediate modal state changes and interruptible artwork/reorder motion | CPU-stressed main-thread pacing has outliers; queue reorder uses moves rather than drag | Interaction intent/focus/recovery are coherent. Universal motion smoothness is not claimed |
| Mobile/responsive design | 8.5 | Actual Chrome captures at 320×568, 390×844, 430×932, 844×390 and 768×1024; no inspected horizontal overflow or hidden categories | Physical touch, keyboard occlusion, real orientation and lock-screen playback remain unverified | Emulation supports the layout quality but cannot establish the intended Android experience. Owner deferred the required device checks |
| Design-system consistency | 9 | One loaded component vocabulary plus isolated player rules, shared tokens, quiet collection identity and reduced-motion/input states | Player styling is still substantial; obsolete unlinked CSS has been archived | Active rules and reusable concepts are predictable. A smaller source archive and further component extraction would improve maintenance |
| Accessibility | 8.5 | Axe checks, contextual names, native dialogs, keyboard Search/menu/focus checks and a regression preserving queue-rail focus during Media Session pause | Full TalkBack and real text scaling/browser zoom are unperformed | Automated checks and manual keyboard/AX inspection establish a strong foundation, not complete assistive-technology certification |
| Frontend architecture/state management | 9 | Private controllers, pure listening projection, bounded/fenced favorite state, preserved receipt kernel, explicit session clocks and serialized play intent; one persistent audio element | Catalog and saved-library controllers still contain substantial rendering code | Authority and state transitions are explicit and failure-aware. Further extraction should follow concrete reuse, not a framework score |
| Performance | 8.5 | Real 10k bounded pages, cancelled reads, coalesced artwork/favorite reads, queue-order p95 302.853→39.160ms and current normal animation rAF p95 16.7/8.9ms | Current CPU4x/80ms network startup p95 reaches 1.75/2.21s; motion retains 25.2/41.7ms p95 and about108ms p99 main-thread intervals | Normal operation is fast. Stress tails and device confidence prevent an unconditional 9; rAF is not compositor FPS and actual device profiling remains necessary |
| PWA quality | 8.5 | Versioned shell-only caching, network-only music/data, truthful capability states, independent update badge and no playback-interrupting activation | Actual Android installation/shortcut, app lifecycle and background behavior remain unverified; LAN HTTP lacks secure-context worker support | Implemented lifecycle boundaries are strong, but the intended phone experience is not evidenced. Platform restrictions are reported rather than bypassed |
| Backend/frontend integration | 9 | Real catalog, Range playback, receipt/revision fencing, atomic collection writes and exact occurrence identity pass browser/PostgreSQL checks | Conflicts across simultaneous listeners remain visible rather than automatically reconciled | UI behavior preserves authoritative contracts and defined failures. More convenience must not borrow stale selection authority |
| Originality/Resonance identity | 9 | Original wave mark and collection wave covers, name initials, owned-library navigation, artwork priority and spatial player continuity | Editorial typography and standard music controls are familiar; generated sleeve family is finite | Identity now lives in collection presentation and listening behavior as well as photography. A 10 would require a more exceptional and broadly validated expression |

## What still prevents the requested acceptance

1. **Mobile 8.5:** Android Chrome phone tests are deferred, including keyboard,
   rotation, large text, touch and lock-screen audio. No emulator result is
   substituted for them.
2. **Accessibility 8.5:** TalkBack and real text scaling/zoom journeys have not
   happened. AX naming and keyboard checks cannot prove their result.
3. **Performance 8.5:** Normal frame pacing supports the isolated artwork
   layer, but stress tails remain. A smaller ambient rendering experiment did not
   produce a reliable benefit and was rejected; raw results were retained.
   A controlled same-process style comparison found only modest differences:
   CPU4x open-event-to-rAF p95 66/61.2ms with local styling versus67.8/70ms
   with the restored root selector (390/1440). Observed cadence changed between
   older runs; no whole speedup is attributed to code. Further high-confidence
   decisions need an actual device/compositor profile, not another inferred FPS
   claim. Android hardware/version evidence is deferred by the owner.
4. **PWA 8.5:** Physical lifecycle checks are deferred. Chrome's install promotion
   requires HTTPS; a service worker requires HTTPS or localhost. Plain LAN HTTP
   cannot be treated as localhost. Browser menus may offer a shortcut even when
   install promotion is unavailable. No insecure-origin flags or certificate
   bypasses were introduced. See [Chrome install criteria](https://web.dev/articles/install-criteria)
   and [service-worker lifecycle](https://developer.chrome.com/docs/workbox/service-worker-lifecycle).

These are explicit open items. The quality target remains open. An independent
review should attempt to invalidate the 9s as well as reproduce the sub-9 gaps.
The inherited Windows file-symlink privilege gaps also remain in the acceptance
ledger; this frontend pass does not fabricate their closure.

## Design studies, implementation and evidence

Three new [quality studies](quality-studies/README.md) informed the queue/source,
visible phone navigation, Search context and playlist composition. They remain
design tools. Only actual application captures are scored.

[Component/state ownership](quality-component-system.md) records the active CSS,
tokens, modules and motion. [ADR-010](../../adr/ADR-010-m2-listening-and-collection-quality.md)
records additive API behavior and preserved authority. The [M2 API contract](../../api/M2-product.md)
records bounds, read projections, PWA and host separation.

Current screenshots, flow notes and measured results are in the
[final capture index](quality-final/README.md) and
[machine-readable verification](../../benchmarks/M2-quality-escalation-verification.json).
Prior failures and interrupted campaigns remain local raw records and are never
replaced by successful reruns. The 62-case full real-browser campaign passes,
followed by a 30-case final hierarchy/accessibility follow-up; neither is used
as a numerical design rubric.
