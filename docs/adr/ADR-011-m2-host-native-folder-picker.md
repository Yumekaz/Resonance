# ADR-011: M2 host-native Windows folder selection

## Context

The owner explicitly requested a normal folder picker instead of repeatedly
typing host paths. Browser directory handles/upload controls do not supply the
absolute server path needed by Resonance’s enrolled-root scanner and watcher.
The target host is an interactive Windows laptop; administration is loopback-only.

## Decision

Add a native Windows `IFileOpenDialog` with `FOS_PICKFOLDERS`, filesystem-only,
existing-folder, unchanged-working-directory and no-recent-document flags.
The host page requests it through a guarded, JSON-only loopback POST. The chosen
path fills the form; enrollment remains a separate Add folder operation with all
existing canonicalization, overlap, root identity and scan rules intact.

Run the dialog on an STA thread in a short-lived helper mode of the same Go
executable. Only fixed arguments are launched, with no shell, script or browser
path input. Database credentials are excluded from the helper environment.
One picker may run per host-handler instance. Output is bounded to 8 KiB and
strictly decoded; unsupported/nonlocal paths are rejected. Request timeout or
disconnect terminates the helper, separating modal UI lifecycle from the server.
A parent-held control pipe also closes on server exit/crash; its EOF terminates
the helper so a hard server stop cannot leave the dialog orphaned. A real
cross-process pipe regression verifies that lifetime boundary.

Native UI requires Windows and an interactive user session. Other/headless
hosts retain explicit path entry. No native browser extension, desktop wrapper,
new package, arbitrary directory-list API, file upload or migration is introduced.
The music listener never exposes the picker route or chosen path.

## Alternatives considered

- Browser upload/directory picker: cannot safely enroll a durable host path and
  would change this into a copy/import workflow.
- Native dialog in a server goroutine: modal COM lifetime and cancellation are
  harder to isolate from ongoing playback and host HTTP requests.
- Shell/PowerShell dialog: an unnecessary runtime dependency, with the older
  framework dialog differing from the requested modern Windows picker.
- General filesystem browser endpoint: expands the path-disclosure surface.

## Consequences

Suggested names follow folder changes until the operator edits the name, and
respect the server’s 128-byte UTF-8 limit. Cancel preserves the folder/name.
Form cancellation aborts a pending choice and rejects a late result. Manual
entry stays accessible. Selection never enrolls, copies or scans the folder.
Native folder selection is Windows-specific and has a two-minute timeout.

## Evidence

The owner selected folders using the actual Windows dialog; the Host form showed
the corresponding path/name. Native Cancel was then confirmed in the live form
with the previous path unchanged. The first Cancel report did not match the
observed new selection, so it was repeated rather than counted as a pass.
Original screenshots remain local because they show private host paths.

Go tests exercise real COM dialog creation/options plus HTTP authorization,
result validation, single-flight, cancellation and output bounds. Browser tests
control only the native-result transport for repeatable Unicode, failure,
pending-cancel and accessibility coverage; they are not claimed as native UI
evidence. Final counts are in the M2 evidence ledger.

References: [IFileDialog](https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nn-shobjidl_core-ifiledialog),
[dialog options](https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/ne-shobjidl_core-_fileopendialogoptions).

## What would cause reversal

A concrete loopback/Origin bypass, orphaned modal process, unbounded output or
unsafe root enrollment regression requires disabling native selection while
retaining the validated manual enrollment flow. A separately authorized host
platform requirement may add a native adapter without changing this boundary.
