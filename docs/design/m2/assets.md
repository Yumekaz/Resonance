# Resonance app assets

The active listener uses six self-hosted font files from Cormorant Garamond
and Inter and unmodified Phosphor SVG icons. Their complete redistribution
licenses are included in `web/assets/licenses/`. Package versions are pinned
in package-lock.json.

The wave mark, app icons, missing-art illustration and ten photographic sleeves
were generated specifically for Resonance with the built-in image-generation
tool. The [sleeve manifest](asset-catalog-v2.json) records delivered hashes,
dimensions and the generation prompts. Delivery uses the listed WebP files;
duplicate raster masters and obsolete alternatives are retained locally.
Prompts for the current sleeves are in production-sources/; the original wave
and fallback briefs are refinement-mark-prompt.md and refinement-fallback-prompt.md.

Generated sleeves are fallbacks only. An embedded PNG/JPEG from the user's
music always takes priority when it passes the bounded extraction, hash and
image checks. Album/artist cards derive art from actual member Tracks; generated
assets do not assert an artist or album identity. Playlist waves and initials
are code-native collection marks.

The three [quality studies](quality-studies/README.md) are clearly labelled
explorations. They are not real product screenshots. Current accepted app
captures are in quality-final/; no user-owned music or cover art is published
as verification media.
