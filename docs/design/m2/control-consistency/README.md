# Actual M2 control captures — 2026-10-04

These are saved, inspected in-app-browser captures of the running Go application
with a real PostgreSQL fixture library. The owner’s songs and artwork are excluded.
The [review](../control-consistency-review.md) records the complete inspection,
before/after changes, keyboard behavior and remaining platform/device limitations.

1. [Desktop Sort](sort-desktop.jpg): selected order, checkmark and anchored menu.
2. [Desktop saved-song player](player-desktop.jpg): favorite beside identity,
   album access, playlist/queue actions and real volume controls before playback.
3. [Phone saved-song player](player-phone.jpg): same controls with a bounded cover
   and coherent mobile hierarchy.
4. [Phone playlist destination](playlist-menu.jpg): the same menu component in
   the player’s dark dialog context.

The original capture bytes are JPEG. The provider’s output dimensions differ
from the requested viewport; [capture-index.json](capture-index.json) records both.
Exact responsive geometry is additionally checked in the browser regressions.
Compression is not assessed as a product defect. No screenshot was raster edited.

Two earlier player captures were rejected because the artwork expansion had not
finished. Their originals and hashes remain in local raw evidence; the index
records those rejected attempts. They are not examples of the settled layout.
