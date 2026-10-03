# ADR-009: M2 listener product and host-local administration

## Context

M2 is explicitly authorized on 2026-09-29. The historical M1.7 ledger still has physical-phone and Windows symlink evidence gaps; this decision does not close them. M1 supplies catalog, root fencing, Range delivery and durable personal-library transactions. Its browser exposes most actions at once and has no search or host-management surface.

## Decision

Keep vanilla browser components and embedded assets in the Go executable. Preserve the browser-owned audio element, cumulative reports, queue selection tokens and receipt retry behavior. Separate navigation/rendering, player controls, personal-library interactions and PWA lifecycle. No framework, production Node process or schema migration is required.

Add bounded case-insensitive literal substring catalog search over existing metadata. Return existing opaque catalog identities in three groups with deterministic display-key/ID ordering. Limit queries to 120 Unicode characters / 512 bytes and each group to 50 entries; cancel with the HTTP request and a three-second deadline. Measure the existing 10k fixture before considering indexes.

Host administration uses a second HTTP listener in the same process, default `127.0.0.1:8081`, accepting only explicit loopback IP addresses. Its routes are absent from the music listener. Every host request additionally checks the real peer and local socket, exact loopback Host/port, Origin and Fetch Metadata. Writes require JSON and a custom header; no CORS relaxation is provided. Forwarded headers grant no authority. The host page is never cached by the listener service worker. Local malicious processes and a deliberately configured local reverse proxy remain outside this boundary; never proxy the admin port.

Folder entry is explicit host-local path entry, not a browser folder handle. Validate absolute non-network paths before existing canonicalization, readable-directory checks, overlap rejection and root identity capture. No filesystem browsing or deletion endpoint. Verification is an explicit rebind action. Scans still use the authoritative scanner and its global lease; manual scan never bypasses verification or quarantine. Return paths only in the separate host response type.

The listener caches only an allowlisted static app shell, including bundled decorative sleeves. APIs, embedded media artwork, audio, Range requests, host pages and writes stay network-only. A waiting worker activates on a later app lifecycle, never by forcibly reloading active playback. LAN HTTP retains browser playback but may lack service workers/installability; M3 owns HTTPS and remote security.

## Alternatives considered

- Same-listener admin routes: a guard regression could expose filesystem operations to LAN users.
- Browser directory picker: does not supply a trustworthy server path.
- Native shell, frontend framework, search service or new index: no current evidence earns the deployment or migration cost.
- Replacing the accepted player/queue model: unnecessary risk to established retry and history behavior.

## Consequences

Operators use a separate local URL; listeners never see host navigation. Two ports share one executable and database. Local administration requires a direct loopback connection. Search is basic lexical matching, not fuzzy or semantic retrieval. Group identities retain M1's conservative local evidence semantics.

## Evidence

See the M2 evidence report as checks are executed. A design study is not an implementation screenshot. The initial image-generation quota interruption is preserved in the prompt record. The restored allowance enabled both missing studies; the three-direction comparison is now complete in `docs/design/m2/direction-comparison.md`. Listening room remains the selected base. Physical-phone and inherited Windows symlink evidence remain separate gates.

## What would cause reversal

A concrete boundary bypass, measured search cost at 10k, unmanageable frontend coupling, or a later authenticated deployment requirement requires a new decision and regression evidence.
