# Whole collections and collection editing

This pass implements the owner's requested order: whole-library continuation,
bulk queue/playlist editing, then playlist search/sort/density. It does not close
the separate all-category 9/10 quality target or advanced physical-phone gates.

| Before | After | Why |
| --- | --- | --- |
| Play followed only the visible 50 catalog rows | Durable full-source references with a rolling queue; the player names the source and total | Next, shuffle and repeat reach later pages without sending 10,000 rows to the browser |
| Repeated organization required individual writes | Select shown/all listed songs; play, copy, move, reorder and remove a group atomically | Duplicate occurrences survive; stale/invalid/full destinations save no partial edit |
| Playlist detail had limited browsing controls | Literal search across all entries, six view orders and two row densities | Finding and viewing music does not silently change its saved sequence |
| Added selection tools pushed phone queue content behind the player | Selection starts in the page header; its editing tools appear only when used | The first upcoming song remains reachable above the persistent dock |
| Player could preview a rolling window as the whole next round | Source continuation and source cycles have distinct copy; unmaterialized future order is not invented | What the player promises matches actual advancement |

These are real Chrome captures of the separate fixture catalog, not generated
mockups. Fixture songs use legal generated WAV data; the existing fallback
artwork system remains separate from embedded covers. No owner music, host paths,
phone photographs or credentials are included.

- [Playlist, desktop](playlist-desktop.png)
- [Playlist, phone](playlist-phone.png)
- [Selected queue songs, desktop](queue-bulk-desktop.png)
- [Selected queue songs, phone](queue-bulk-phone.png)
- [Whole-source player](whole-source-player.png)

Reviewed sizes: 320×568, 390×844, 430×932, 844×390, 768×1024 and 1440×900.
No horizontal overflow was observed. On small/short screens, playlist controls
and songs require vertical scrolling; emulation does not establish actual device
keyboard, installation, locked-screen audio or TalkBack behavior. The queue
selection captures are scrolled to the editing controls and selected rows.

Keyboard menu opening, Escape/focus return, source/view order agreement, bounded
rows and automated accessibility checks are covered by the browser tests.
See [verification](../../../benchmarks/M2-collections-verification.json) and
[ADR-012](../../../adr/ADR-012-m2-collection-playback-and-bulk-editing.md).
