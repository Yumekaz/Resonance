# Park M2 and resume later

The owner closed M2's functional scope on 2026-10-07 and deferred accessibility
and device-performance follow-ups in the strict review. Parking stops the local
listener, Host and development PostgreSQL instance; the URLs are unavailable
until the runtime is rebuilt and restored. M3 is not started automatically.

Keep the tracked repository and its Git history. The ignored `.resonance-local/`
directory holds the compact, private recovery bundle: current catalog/queue/
playlist/favorite/history/source state, local configuration, recovery manifest
and retained evidence. Never publish that directory. Actual music folders stay
at their existing locations and must not be deleted by repository cleanup.

Before removing bulky development files:

1. Stop only the repository-owned app process so persistent writes settle.
2. Take a fresh full PostgreSQL custom-format backup of the current main database.
3. Restore it into an isolated database; compare all persistent table contents
   and the migration ledger before startup, and verify the version-9 schema.
4. Retain backup hashes, private connection settings and enrolled-folder locations
   in the ignored bundle. Preserve compact evidence rather than replacing raw
   failures with only passing summaries.
5. Stop the development PostgreSQL cluster cleanly. Remove only verified
   workspace-local, rebuildable caches, tool downloads, generated test corpora,
   obsolete preview binaries and disposable database clusters. Do not delete
   enrolled personal music, tracked assets/source/tests/docs or Git history.

The local bundle also retains one matching app executable and PostgreSQL CLI
runtime, so an offline restore can use `.resonance-local/resume.ps1` without
reinstalling the removed compiler/test caches. Its default listener is loopback;
explicitly choose the current trusted LAN address for phone use.

To resume from source or begin M3:

1. Install Go 1.25+ and PostgreSQL 17 using the existing setup runbooks. Run
   `npm ci` only if browser development/tests are needed; Node is not required
   for the shipped application.
2. Create a fresh local PostgreSQL cluster and owner/database using the private
   configuration retained in `.resonance-local/`. Restore the verified dump with
   `pg_restore --exit-on-error` into an empty database.
3. Rebuild the app with `go build -o bin/resonance.exe .`. Set the private
   `RESONANCE_DATABASE_URL`; do not print its password or commit it.
4. Run the executable with `-migrate-only`, then start on the configured trusted
   LAN address with a separate loopback-only Host listener. Keep existing music
   folders in place; verify/rebind roots explicitly if their identity changed.
5. Confirm `/ready`, catalog reads, indexed Range playback, queue and playlist
   state before starting new milestone work. A database backup does not include
   music bytes or prove another computer has the original folders.

The private manifest records the exact backup/configuration and cleanup results
for this machine. Restore instructions and hashes should remain available even
when caches and temporary tools have been removed. The deferred quality work is
still unverified; release acceptance does not turn it into a passing score.
