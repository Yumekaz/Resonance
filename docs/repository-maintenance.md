# Repository maintenance

The maintained tree contains the Go application and migrations, active listener
and host assets, licensed media fixtures, regression/benchmark tools and accepted
sanitized evidence. Historical milestone code and tests are not unused simply
because M2 is active. The M0 diagnostic player remains supported at `/demo`.

## Cleanup boundaries

- Remove obsolete preview executables after checking the configured executable
  and running processes. Keep the current build until its replacement is ready.
- Remove installer archives only after validating the extracted runtime and its
  callers. Never remove an active toolchain or PostgreSQL installation.
- Keep databases, enrolled music, credentials, migrations, failed-attempt records,
  raw verification logs, accepted captures and original generated artwork sources.
  These have operational or evidence value even when excluded from Git.
- Treat build caches and installed dependencies as rebuildable development state,
  not public source. Deleting them just before verification only recreates them.
- Remove source/asset candidates only after tracing HTML, JavaScript, CSS,
  dynamically constructed names, tests and the service worker. Active navigation
  uses computed `-fill` icon names; a literal-only search misses these.
- Update the worker's explicit precache list when removing a static asset. Old
  workers retain their coherent cached version until old tabs close; do not force
  activation over playback for a maintenance task.
- Use a reviewed allowlist rather than a blanket Git clean. `data/` contains live
  state and must never be treated as disposable merely because it is ignored.

## Verification

Run `npm run test:frontend` for the pure frontend-state/order checks. Run the
configured Playwright suites against a separate disposable catalog and host
listener; the owner library is not a test fixture. Go format/vet/unit and the
serial PostgreSQL integration campaign cover the embedded assets and retained
M0–M2 contracts. Preserve failures and report privilege/opt-in skips separately.

The 2026-10-04 cleanup removes unused classic-controller bindings/exports, a
retired hidden playlist selector, its no-op refresh calls and five unused icons.
It declares the collection pagination cursor locally and enables strict mode in
that controller, preventing the previous implicit `window.viewCursor` leak.
The searchable playlist picker, playback/session clocks, authoritative queue
and public APIs retain their behavior.

Local disk cleanup removes obsolete executables, redundant installer downloads,
an unused duplicate SDK and an empty literal Windows cache directory. Private
plans/deletion inventories stay local; public evidence contains summaries only.

## 2026-10-04 result

The [sanitized verification](benchmarks/M2-hygiene-verification.json) records
116 obsolete executables, two installer archives, one unused duplicate SDK and
an empty literal cache tree removed: approximately 2.62 GiB of logical file size.
The active build, PostgreSQL installations/data, music, dependency/build caches,
source media fixtures and raw historical evidence are retained.

Final Chrome campaign: 74/74, no skipped/flaky cases. Frontend state: 10/10.
Go format/vet/unit and serial real-PostgreSQL integration passed with recorded
privilege/opt-in skips. The initially stopped database and failed verification
attempt remain documented; successful reruns do not erase them.
