# ADR-002: Explicit single Range streaming for M0

## Context

Browser seeking needs byte access to one file. The server must expose selected intervals, accepted byte counts, cancellation, and incomplete transfers.

## Decision

Keep the small single-range parser and use one opened/statted file per request. Full GET returns 200. Bounded, open-ended, and suffix byte ranges return 206 with exact inclusive Content-Range and Content-Length. Ends and suffixes larger than the file clamp safely, including numerals beyond int64. Unsatisfiable/reversed intervals return 416; malformed single-byte syntax returns 400.

Ignore multiple/repeated Range fields and unsupported units as a whole. HEAD ignores Range. No validators are advertised, so If-Range causes a full 200 response. Set media Cache-Control to no-store. These policies follow RFC 9110's allowance to ignore Range; multipart and cache validation are not implemented.

Copy with a bounded 32 KiB buffer, checking cancellation between operations and setting a rolling 30-second write deadline. Close the file on all exits. Abort an already-started response on failure; never append an error document to media bytes. Log the original Range field separately from selected offsets and distinguish presence from application.

## Alternatives considered

- `http.ServeContent`: provides general Range and conditional handling but would need careful wrappers for interval and copy-error telemetry. Prefer it if protocol requirements grow; the explicit implementation needs regression coverage to remain defensible.
- HLS/DASH: no M0 segmentation requirement.
- Full-file buffering: violates the streaming requirement.

## Consequences

The configured file must remain immutable. Truncation causes an incomplete transfer; same-size in-place edits are not reliably detected. Accepted write bytes are not a delivery or audible-playback guarantee. Cancellation can occur after bytes have entered socket buffers. A paused browser can continue downloading.

## Evidence

HTTP tests cover edge intervals, huge numerals, repeated fields, HEAD, If-Range, exact bytes and lengths. Real-socket disconnect testing verifies handler completion and file release. A deterministic truncation test verifies abort/logging. Browser tests disable cache, throttle transfer, measure received bytes, and validate a new partial response after seeking outside the buffered interval.

## What would cause reversal

Multipart ranges, validators, or further parser growth would favor ServeContent. No future transport is implemented here.
