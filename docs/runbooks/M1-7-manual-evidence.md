# Remaining operator evidence for M1.7

## Physical same-LAN phone journey — G2

Use a real phone and the current corrected build on a trusted private LAN. Record the UTC date, phone model/OS, browser version, build identity, and screenshots or a recording. Earlier M1.4 phone evidence and browser emulation do not satisfy this M1.7 gate.

1. Browse actual imported Artist → Album → Track groups and record the logical Track IDs.
2. Play audible media, record playback progress before full download, and seek to an unbuffered position. Correlate the seek with a new `206` response and its request ID in the path-free server log.
3. Use Play Now, Play Next, Add to Queue, Next/Previous, queue reorder/remove, playlists, and favorite/unfavorite. Record queue occurrence IDs, playlist IDs, and versions where relevant.
4. Produce qualifying listening history through actual playback, including a natural end. Browser reports remain client evidence; the operator's audible observation must be recorded separately.
5. Restart the server and reload the phone. Verify the durable queue/playlist/favorite/history state and logical IDs survive; a reload must not claim live playback or autoplay the persisted selection.

Keep filesystem paths, native root identity, database credentials, dumps, and personal listening data out of the public evidence. Store the correlated, sanitized result beside the public M1.7 report. Leave G2 NOT PERFORMED until this record exists.

## Windows file-symlink capability — G6/G8

The restricted runner and the host account both failed to create a file symlink. The host probe called `CreateSymbolicLinkW` with `SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE` and returned **Win32 error 1314**. The host token did not have `SeCreateSymbolicLinkPrivilege`. No Windows setting or policy was changed by the closure campaign.

Provide either an elevated Administrator console/token with `SeCreateSymbolicLinkPrivilege`, or an environment that permits unprivileged symbolic links through Windows Developer Mode. Use a disposable PostgreSQL database and supply its credentials privately as described in the bootstrap runbook; do not put the password in the test command or report.

Run from the source checkout with Go 1.25 and Windows PowerShell on the explicit PATH:

```powershell
go test -json -p 1 -parallel 1 -tags=integration -count=1 `
  -run '^(TestReviewMediaSymlinkCannotEscape|TestWindowsKnownLocationReplacedBySymlinkIsDirectlyUnavailable)$' `
  . ./internal/library
```

Both named tests must report PASS with **zero skips**. Preserve this attempt separately from the existing privilege-blocked attempts, update G6/G8, and regenerate a new public export without modifying the original private raw artifacts.
