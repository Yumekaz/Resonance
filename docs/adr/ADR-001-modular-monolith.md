# ADR-001: One process for M0

## Context

M0 needs one configured audio file, three HTTP endpoints, and one browser page. No measured requirement needs independently deployed components.

## Decision

Use one Go net/http process and an in-memory logical-ID registry. Keep Range parsing and handlers in separate files within one module. Embed the three web assets rather than exposing a working-directory file server. Use Go 1.24's root-confined file opening for media; the operator enrolls the configured file's parent directory.

## Alternatives considered

- Database: M0 has no durable catalog state.
- Separate media and web services: no independent lifecycle or scaling requirement.
- Client framework: plain browser APIs cover this player.
- Plain os.Open/FileServer on mutable paths: insufficient containment against links to other host files.

## Consequences

One executable serves the UI and media. A trusted PCM WAV must be configured separately. No authentication or public Internet access feature exists. Loopback is the default; a trusted private LAN address can be selected for phone testing. Client input never becomes a filesystem path. Root containment does not sandbox a privileged local operator or malicious mount/hard-link setup.

## Evidence

The Go suite exercises HTTP contracts, Windows-style path attacks, embedded assets, and Windows junction containment. Browser tests exercise the same server over HTTP. Benchmark results remain local to the measured workload.

## What would cause reversal

A concrete persistence, deployment, or scaling requirement would need a separate decision. None is introduced by this M0 review.
