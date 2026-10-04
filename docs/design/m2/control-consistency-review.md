# M2 control consistency review — 2026-10-04

This pass responds to the owner’s dropdown, speaker and player-placement reports.
The source of truth is the running Go application, fresh in-app-browser captures,
and real PostgreSQL playback/library workflows. The supplied player study is a
placement reference; it is not verification evidence.

| Before                                                                                                 | After                                                                                                                                                                          | Why                                                                              |
| ------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------- |
| Sort opened an operating-system popup with unrelated blue selection styling.                           | Sort and playlist destinations share the listener’s anchored menu, selected checkmark, palette and focus treatment.                                                            | The opened control now belongs to the same interface as its trigger.             |
| The speaker was a noninteractive span.                                                                 | Both players have actual mute/unmute buttons and accurate volume controls.                                                                                                     | A visible speaker control performs its expected action without pausing the song. |
| Player gain changed only through the compact slider.                                                   | Both controls follow the persistent audio element’s `volumechange`, including mute, zero volume and external element changes. The preference survives reload without autoplay. | The audio element owns the state rather than independent UI values.              |
| The favorite heart was below transport.                                                                | It sits beside the title/artist/album block above the timeline.                                                                                                                | It follows the selected reference and stays attached to the song identity.       |
| A saved selection showed artwork but disabled favorite/playlist actions and omitted album information. | A separate prepared display track supports those actions before playback; browsable albums are links and ungrouped album titles remain plain text.                             | Browsing a saved selection must not require starting a listening session.        |
| Delayed metadata could leave a previous song’s cover visible.                                          | Selection changes clear the old cover; generation checks prevent delayed responses changing newer metadata/action targets.                                                     | Song identity, cover and action destination must agree.                          |
| Desktop Previous/Next labels could be clipped, and phone artwork pushed controls down.                 | Labels appear where the control group has sufficient width; smaller layouts use icons. Phone artwork reserves room for actions and volume.                                     | The player must fit its actual column, including the narrow desktop breakpoint.  |
| Menu focus waited for an animation frame.                                                              | Select menus initialize synchronously; other menus initialize in a microtask. Escape/Tab restore focus and typeahead navigates choices.                                        | An immediate keypress must not race menu readiness.                              |

## Screenshot-led inspection

Original owner-library captures and unsuccessful attempts remain in the ignored
local `data/m2/control-review/` directory. Owner music/artwork is not published.
The public [capture set](control-consistency/README.md) uses the separate real
fixture library. Screenshots were saved, opened and visually reviewed before
acceptance.

| Step | Surface and check                          | General health                                                                                                |
| ---- | ------------------------------------------ | ------------------------------------------------------------------------------------------------------------- |
| 1    | Desktop Artists and Sort, before/after     | Native popup replaced; menu aligns with its trigger and marks the selected order.                             |
| 2    | Saved selection, desktop player            | Heart placement and contextual actions resolved; no autoplay.                                                 |
| 3    | Phone player, 390×844 and 430×932          | Artwork and control density resolved; long metadata remains bounded.                                          |
| 4    | Small phone, 320×568                       | Horizontal fit checked; vertical scrolling is intentional for long song details.                              |
| 5    | Landscape, 844×390                         | Transport remains reachable; the full player can scroll vertically.                                           |
| 6    | Tablet portrait, 768×1024                  | Two-column player checked; controls stay inside their information column.                                     |
| 7    | Narrow desktop, 1101×900, and 1440×900     | Control geometry checked at the rail breakpoint and full desktop layout.                                      |
| 8    | Mobile Library/Sort                        | Selected state, menu positioning, collection navigation and dock inspected.                                   |
| 9    | Loaded mobile Search and App options       | Result context, query clearing, dock and dialog inspected; no new defect established.                         |
| 10   | Desktop Up Next                            | Saved queue and compact player inspected; local ready state remains distinct from playback on another client. |
| 11   | Playlist destination from a saved song     | Styled choice menu, real add operation and focus return verified without a listening session.                 |
| 12   | Immediate keyboard menu dismissal and mute | Escape/Tab/arrow checks plus real audio continuity and synchronized controls verified.                        |

## Implementation and verification

`select-control.js` progressively enhances the existing native select values and
change events using the shared contextual-menu surface. Dynamic option changes
invalidate an open menu. `player-volume.js` owns player gain/mute UI and local
preferences; the existing persistent audio element remains authoritative.
Prepared display metadata is separate from active playback in `player.js` and
cannot grant selection authority or create playback history.

Four unmodified, licensed Phosphor icons supply caret, selection and speaker
states. The new scripts/icons are explicitly included in the static PWA shell.
Artwork expansion cancels on viewport changes so its old geometry cannot cover
the resized player. Menu motion uses existing reduced-motion and keyboard rules. No new decorative
image was needed for these control/layout defects.

The [sanitized verification record](../../benchmarks/M2-controls-verification.json)
contains exact final counts, commands, preserved failures and raw evidence hashes.
Automated accessibility checks complement the inspected accessibility tree and
keyboard flows; they do not establish full assistive-technology compliance.

## Platform boundary and remaining evidence

Player volume is not device master volume. The web media API exposes the media
element’s gain/mute, not a portable system-volume read/write interface.
`volumechange` is not a device-volume observer. The UI therefore says “Player
volume · device volume is separate.” When the browser rejects setting player
gain, it hides the ineffective slider and directs the listener to device buttons.

References: [HTML media volume](https://html.spec.whatwg.org/multipage/media.html#dom-media-volume),
[volumechange](https://developer.mozilla.org/en-US/docs/Web/API/HTMLMediaElement/volumechange_event),
[Apple’s platform limitation](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/Using_HTML5_Audio_Video/Device-SpecificConsiderations/Device-SpecificConsiderations.html).
The gain-rejection test is capability fault injection, not physical iOS evidence.

The owner deferred Android Chrome testing. Physical touch, hardware-volume,
installation, background audio, large text and TalkBack remain unverified.
No new load/frame-time benchmark was performed and this pass makes no new
performance claim. This targeted review does not close M2’s all-category quality
or inherited engineering gates.
