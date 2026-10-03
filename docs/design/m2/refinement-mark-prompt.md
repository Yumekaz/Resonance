# Resonance production logomark

Generated with the built-in image generation tool on 2026-09-30. Reference inspected: `docs/design/m2/listening-room-study.png`.

Source preserved: `local generation archive (original retained privately)`

Production copy: `web/assets/resonance-mark.png`.

## Exact prompt

```text
Use case: logo-brand
Asset type: original production raster logomark for Project Resonance website navigation and branding, transparent PNG.
Primary request: Create one elegant original Resonance symbol consisting of exactly two graceful offset acoustic wave curves, inspired by the warm Listening room design reference. Each curve travels left to right in a smooth organic sine-like sweep, with a soft rising crest toward the left and a low trough toward the right; the second curve is shifted down and subtly left. The curves feel drawn with a refined tapering brush, but have clean crisp edges and no paper texture.
Style/medium: restrained editorial identity mark, vector-like flat silhouette rendered as a raster asset.
Color palette: single dark terracotta rust, approximately #963E2C.
Composition/framing: square 1024 by 1024 canvas, centered symbol occupying about 70 percent of canvas width and about 35 percent height, generous clear padding on all sides.
Scene/backdrop: genuinely transparent background with alpha.
Constraints: exactly two separate offset wave strokes; no text, no letters, no surrounding badge, no shadow, no gradient, no glow, no mockup or presentation; use only the warm rust curves on transparent background; avoid adding an extra wave or decorative dots.
```

## Inspection

The result contains two elegant tapered rust wave strokes with no text, badge, or extraneous decoration. The PNG preserves actual transparency, checked at its corner. The tool returned a 1254 × 1254 image despite the requested 1024 × 1024 framing; the original size is preserved. The silhouette occupies more width than requested and has generous vertical padding, so the consuming layout should size or crop its presentation deliberately. No pixel editing or resampling was applied.

## Production PWA icon family

Generated with the built-in image editing tool on 2026-10-01, using `web/assets/resonance-mark.png` as the identity reference. The earlier interrupted call returned no saved result. Both completed generation outputs are preserved:

- First pass: `local generation archive (original retained privately)`
- Selected refinement: `local generation archive (original retained privately)`

### Exact first-pass prompt

```text
Use case: precise-object-edit
Asset type: production PWA app icon master for Project Resonance, square 1024 by 1024 pixels.
Input image: existing transparent Resonance logomark, identity reference and edit target.
Primary request: Retain the same two graceful tapered dark rust acoustic wave curves from the reference. Put those curves centered on a plain, entirely flat warm ivory square background, approximately #F7F3EB. Reduce the symbol to occupy 65 to 70 percent of the square width, with generous clear margin so every part of both curves remains inside the central circular maskable safe area. Center the curves horizontally and vertically as a unit. Preserve the visual identity, rust color and elegant wave silhouettes. This is the actual exported app icon image, not an app icon shown in a device or a presentation.
Constraints: exactly two curves, no text or letters, no extra shapes, no border, no rounded-corner cutout, no shadow, no glow, no gradients, no texture, no mockup. Full opaque ivory background all the way to all four edges. Make the mark crisp and readable at small app-icon sizes.
```

### Exact refinement prompt

```text
Change only the scale and placement of the rust wave symbol in this app icon. Reduce both rust curves together to 72% of their current size, preserving their exact shape, color, relative placement, and smooth edges. Place the resulting smaller symbol centered horizontally and vertically on the same full square plain warm ivory background. The rust symbol must occupy at most 66 percent of the total canvas width with equal clear margin on left and right. It must fit entirely in the central circular safe area of radius 40 percent of the canvas width. Keep the same two curves and ivory background; no text, no texture, no shadow, no other changes. This is a production maskable app icon, square.
```

### Delivery

Selected master: `web/assets/app-icon-master.png`. Delivery copies: `web/assets/icon-192-v2.png` (192 × 192) and `web/assets/icon-512-v2.png` (512 × 512). Bundled Pillow performs only identical-master Lanczos downsampling for delivery; the generated master is preserved. The first pass was rejected because its rust waves extended beyond the requested safe margin; the selected refinement is centered with generous clear ivory space and no text or extra shapes.

The actual master is 1254 × 1254 RGB. Rust-pixel bounds are x=282–975 and y=497–759; the farthest detected rust pixel is 28.14% of canvas width from its center, inside the 40% circular safe area. The final silhouette occupies approximately 55% of canvas width, smaller than the original requested 65–70%, with crisp readable strokes at the inspected 192-pixel delivery size.

Delivery packaging: original PNG masters are retained in `production-sources/`; the live app uses the optimized `web/assets/artwork-unavailable.webp`, transparent `web/assets/resonance-mark.png`, and 192/512 v2 icon delivery files. Creative content was generated with the built-in image tool; encoding/downsampling only prepared browser delivery.
