# ADR-004: M1.2 initial library import

## Context

M1.2 needs the first durable ingestion path without taking on M1.3 reconciliation or file watching. Operators must enroll local folders safely, import supported audio, preserve Track/MediaObject/MediaLocation separation, and keep M0 playback independent.

## Decision

Add host-only `library add`, `list`, `scan`, and `disable` commands to the existing Resonance executable. No HTTP endpoint accepts a filesystem path. Enrollment canonicalizes the directory, verifies it is readable, rejects exact and nested overlaps under a PostgreSQL advisory lock, stores a UUID and private canonical path, and exposes only ID, name, and enabled state in command output.

Use one bounded scanner process and one scan at a time across the application. Each scan opens the enrolled root with `os.OpenRoot`, skips symbolic links, junctions/reparse points, and irregular entries, and opens files by root-relative name. It admits MP3, FLAC, and PCM WAV. Every unseen location is streamed through SHA-256 using a 32 KiB buffer. MP3 and FLAC pass through the restricted M1.1 metadata adapter; WAV receives a bounded RIFF/PCM envelope check and filename-derived title. Hashing and parsing happen outside database transactions.

Each imported file uses one short transaction. Exact encoded SHA-256 reuses the existing MediaObject and adds a location. New bytes create a Track, MediaObject, and MediaLocation. Raw artist credit, album title, and album-artist credit are stored on the Track; absent values remain null. M1.2 deliberately does not create Artist or Album grouping identities because equal display strings are insufficient identity evidence and per-Track grouping rows would be false identities. M1.4 owns that grouping policy and migration. Artist credits are stored as one string and are not split. Artwork is represented by SHA-256 and MIME only; artwork bytes are not yet persisted.

Scan runs persist counters and at most 100 path-relative error records. Structured logs carry scan and root IDs without absolute paths. Per-file corruption, unsupported formats, permission failures, and disappearance continue the scan. A root-level or database failure fails the run. Ctrl+C cancels the CLI scan; the finalizer records cancellation when PostgreSQL remains available. A later scanner marks an abandoned `running` row as interrupted after acquiring the global worker lock.

## Alternatives considered

- HTTP folder enrollment: would let an unauthenticated web client submit host paths.
- Generic job framework or worker process: M1.2 needs one bounded local worker only.
- Holding a transaction during hashing: would unnecessarily extend locks over file I/O.
- Following links: can leave the enrolled boundary or introduce traversal cycles.
- Treating filenames, titles, or paths as durable identity: they are mutable presentation/location evidence.
- Deletion or rename reconciliation: belongs to M1.3 and would require a broader identity matrix.

## Consequences

Repeated scans skip an already imported root-relative location and never delete unseen rows. They therefore do not detect replacements, moves, renames, or content changes at a known location. Exact copies at different paths share encoded bytes and the first Track identity. Separate tagged encodings of the same recording remain separate Tracks because fingerprinting and semantic merge are out of scope. Artist and Album identity/grouping is deferred rather than inferred from mutable or ambiguous display strings.

The stored canonical path and relative path are private PostgreSQL state. Disabling a root prevents future scans and never removes files or catalog rows. Initial import can use CPU, disk bandwidth, and database capacity; measurements establish a baseline rather than a service-level target.

## Evidence

Real PostgreSQL integration tests cover two roots, overlap rejection, exact-byte deduplication without universal Track/hash coupling, restart persistence, disabled and missing roots, corrupt MP3/FLAC continuation, unsupported files, file disappearance, simulated permission denial, database interruption/recovery, cancellation, bounded errors, global scan exclusion, and Windows junction containment. Migration 3 upgrades populated M1.1 tables without losing legacy rows; corrective migration 4 preserves already-imported raw credits/titles while removing the premature per-Track Artist/Album identities from the original submission.

A 302-file controlled corpus imported 795,948,098 bytes in 1,720.664 ms. The existing M0 Range benchmark overlapped the scan for 577 ms and completed successfully. Full commands, environment, raw output, and limitations are in `docs/benchmarks/M1-2.md`.

## What would cause reversal

M1.3 evidence may justify a durable queue, resumable work, richer artist/album identity, or a different scan concurrency limit. Any such change must preserve bounded resource use and the host filesystem boundary.
