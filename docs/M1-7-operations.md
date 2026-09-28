# M1 single-node operations

## How the running system is put together

Resonance is one Go server and one PostgreSQL database. PostgreSQL stores catalog identity, location observations, grouping, and personal library state. The server streams an already-open media handle over HTTP byte ranges. A Track is the catalog identity, a MediaObject is one encoded byte representation, and a MediaLocation is one enrolled-root location; a pathname is not a durable identity.

The authoritative scanner is the only path that publishes catalog observations or reconciles absence. Filesystem notifications only schedule scans. A root must be explicitly enrolled, identity-verified, and enabled. For a scan that can reconcile absence, the scanner verifies the opened root at traversal start and reopens the enrolled path at traversal end against the persisted identity fence. An unavailable, unverified, quarantined, or mismatched root preserves existing catalog state and cannot infer absence.

PostgreSQL stores queue selection, queue occurrences, playlists, favorites, and qualifying listening-session summaries. The browser owns live playback, buffering, pause state, and position. A reload does not claim active playback or autoplay a selected Track. Listening reports are bounded client evidence; they cannot prove audible sound.

## Operational checks

| Observed state | Meaning | Operator action |
|---|---|---|
| `/health` is 200; `/ready` is 503; library status reports database degradation | The process is alive, but PostgreSQL or its schema is not usable. | Restore PostgreSQL, wait for readiness, inspect path-free library status, then retry the original idempotent user request when applicable. An already-open media stream can continue independently of new database reads and writes. |
| `/ready` is 200; watcher coverage is degraded | Catalog reads work, but automatic freshness is reduced. | Inspect the status error code and root coverage. Restore watcher coverage or run an authoritative host-side scan of a verified, enabled root. |
| A root is offline | The stored catalog is retained; physical media may not resolve. | Restore the same root and allow reconciliation. A database restore does not prove that the root or media bytes remained continuous. |
| A root is quarantined or its identity is unverified | Path binding is not trusted, so absence is not authoritative. | Inspect the host directory. Use explicit host-side identity verification before enabling or scanning it. Do not treat a manual scan as verification. |
| A scan is partial or retrying | Traversal did not establish a complete view. | Keep existing catalog availability; inspect safe error codes and retry after the underlying access problem is fixed. |
| A Track is unavailable | Track and user-library intent remain stored, but no usable enrolled location was found. | Restore or add media under a verified root and let the authoritative scanner converge. Distinguish resolver failure from a browser decoder error. |

`/health` is liveness. `/ready` checks PostgreSQL, schema compatibility, and grouping readiness. `GET /api/v1/library/status` reports bounded watcher and reconciliation diagnostics without paths or native identity data. Host-side `library list`, `library verify`, `library enable`, and `library scan` are described in the [M1.6 status contract](api/M1-6-library-status.md) and [bootstrap runbook](runbooks/M1-7-bootstrap.md).

## Security assumptions and limits

- The listener is unauthenticated and has no TLS. Keep it on loopback or a trusted private LAN; public Internet access and remote access are outside M1.
- Keep PostgreSQL loopback-only and database credentials private. Do not paste database URLs or passwords into logs, reports, or public issues.
- Clients use logical Track IDs. The catalog and user-library APIs do not expose canonical host paths. Indexed playback remains behind the enrolled-root resolver and confinement checks.
- Only host-side enrollment and explicit identity verification establish a root binding. Filesystem events cannot verify or rebind a root.
- Backups cover PostgreSQL state, not original audio, extracted artwork bytes, browser live state, or in-memory watcher intent. On another host or filesystem, inspect and verify roots before enabling scans.
- A streamed response or browser listening report does not prove that a person heard the audio. Browser codec support varies.
- Windows sharing can prevent physically renaming a root while an open handle is held. Injected end-identity mismatch tests exercise the publication fence; they are not evidence of a physical Windows path-rebind during an open scan.
- No RPO or RTO is promised. Server termination can close active connections; PostgreSQL loss and normal server shutdown are separate failure modes.

See the [complete restore procedure](runbooks/M1-7-backup-restore.md), [M1.4 streaming and confinement contract](api/M1-4-catalog.md), and [M1.5 user-library contract](api/M1-5-user-library.md) for the detailed request behavior.
