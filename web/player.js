(() => {
  const audio = document.querySelector("#audio");
  const dialog = document.querySelector("#now-playing");
  const actionDialog = document.querySelector("#action-dialog");
  const syncDialogPalette = () => {
    actionDialog.dataset.playerContext = String(dialog.open);
  };
  new MutationObserver(syncDialogPalette).observe(dialog, {
    attributes: true,
    attributeFilter: ["open"],
  });
  syncDialogPalette();
  let track = null;
  let preparedTrack = null;
  let favoritePending = false;
  const displayTrack = () => track || preparedTrack;
  let transportPending = false;
  let queueStepPending = false;
  let lastQueueSnapshot = null;
  let preparedSelection = null,
    prepareGeneration = 0;
  const el = (id) => document.getElementById(id);
  let modesPending = false;
  function renderModes() {
    const state = window.resonancePlaybackModes.get();
    for (const id of ["keep-playing", "full-keep-playing"])
      el(id).checked = state.continuous;
    for (const prefix of ["player", "full"]) {
      const shuffle = el(prefix + "-shuffle");
      shuffle.setAttribute("aria-pressed", String(state.shuffle));
      shuffle.title = state.shuffle
        ? "Shuffle on · Up Next shows the order"
        : "Shuffle upcoming songs";
      const repeat = el(prefix + "-repeat");
      const label =
        state.repeat === "one"
          ? "Repeat one"
          : state.repeat === "all"
            ? "Repeat all"
            : "Repeat off";
      window.resonanceUI.button(
        repeat,
        state.repeat === "one" ? "repeat-once" : "repeat",
        label,
        true,
      );
      repeat.setAttribute("aria-pressed", String(state.repeat !== "off"));
      repeat.title = label + " · click to change";
      shuffle.disabled = modesPending;
      shuffle.setAttribute("aria-busy", String(modesPending));
    }
  }
  for (const prefix of ["player", "full"]) {
    el(prefix + "-repeat").addEventListener("click", () =>
      window.resonancePlaybackModes.cycleRepeat(),
    );
    el(prefix + "-shuffle").addEventListener("click", async () => {
      if (modesPending) return;
      modesPending = true;
      renderModes();
      try {
        await window.resonanceUser.toggleShuffle();
      } catch {
        el("player-error").textContent =
          "Shuffle could not be saved. Refresh Up Next and try again.";
        el("player-error").hidden = false;
      } finally {
        modesPending = false;
        renderModes();
      }
    });
  }
  window.addEventListener("resonance:modes", renderModes);
  for (const id of ["keep-playing", "full-keep-playing"])
    el(id).addEventListener("change", (event) =>
      window.resonancePlaybackModes.setContinuous(event.target.checked),
    );
  renderModes();
  // Native progress supplies the measured elapsed portion below the native
  // seek input. It is visual-only; the slider remains the accessible control.
  for (const id of ["seek", "full-seek"]) {
    const input = el(id);
    const control = document.createElement("span");
    control.className = "seek-control";
    const progress = document.createElement("progress");
    progress.id = `${id}-progress`;
    progress.max = 1000;
    progress.value = 0;
    progress.setAttribute("aria-hidden", "true");
    input.before(control);
    control.append(progress, input);
  }
  const time = (value) =>
    Number.isFinite(value)
      ? `${Math.floor(value / 60)}:${String(Math.floor(value % 60)).padStart(2, "0")}`
      : "—";
  function refresh() {
    const known = Number.isFinite(audio.duration) && audio.duration > 0;
    for (const prefix of ["", "full-"]) {
      el(prefix + "elapsed").textContent = time(audio.currentTime);
      el(prefix + "duration").textContent = known ? time(audio.duration) : "—";
      el(prefix + "seek").disabled = !known || queueStepPending;
      el(prefix + "seek").value = known
        ? Math.round((audio.currentTime / audio.duration) * 1000)
        : 0;
      el(prefix + "seek-progress").value = Number(el(prefix + "seek").value);
      el(prefix + "seek").setAttribute(
        "aria-valuetext",
        `${time(audio.currentTime)} of ${known ? time(audio.duration) : "unknown duration"}`,
      );
    }
    for (const id of ["play-toggle", "full-play"]) {
      const label = audio.paused ? (audio.ended ? "Replay" : "Play") : "Pause";
      if (el(id).dataset.playState !== label) {
        window.resonanceUI.button(
          el(id),
          audio.paused ? "play" : "pause",
          label,
          true,
        );
        el(id).dataset.playState = label;
      }
      el(id).disabled = transportPending || (!track && !preparedSelection);
    }
    const listening = window.resonanceListening.get();
    const controls = window.resonanceListening.controls(
      lastQueueSnapshot,
      listening.playback,
      preparedSelection,
      audio.currentTime,
      window.resonancePlaybackModes.advanceOptions("next").repeat,
    );
    for (const direction of ["previous", "next"])
      for (const id of [
        direction,
        "player-" + direction,
        "full-" + direction,
      ]) {
        const button = el(id);
        button.disabled = queueStepPending || !controls[direction];
        button.setAttribute("aria-busy", String(queueStepPending));
      }
    if ("mediaSession" in navigator)
      navigator.mediaSession.playbackState = audio.paused
        ? "paused"
        : "playing";
  }
  async function performToggle() {
    if (!track && preparedSelection) {
      try {
        await window.resonanceUser.resumeSelection({ ...preparedSelection });
      } catch (error) {
        el("playback-state").textContent =
          error.code === "stale_selection"
            ? "Queue selection changed. Open the queue and choose a track."
            : "This selection could not play. Choose another track.";
      }
      return;
    }
    if (!track) return;
    const state = window.resonanceListening.get();
    if (state.mode === "finished" && state.selected) {
      try {
        await window.resonanceUser.selectQueueItem(
          state.queue,
          state.selected.id,
        );
      } catch {
        el("playback-state").textContent =
          "This queue changed. Open Up Next to choose a song.";
      }
      return;
    }
    if (audio.paused) {
      try {
        await audio.play();
      } catch {
        el("player-error").textContent =
          "Playback could not start. Choose another track or try again.";
        el("player-error").hidden = false;
      }
    } else audio.pause();
  }
  async function toggle() {
    if (transportPending) return;
    transportPending = true;
    for (const id of ["play-toggle", "full-play"])
      el(id).setAttribute("aria-busy", "true");
    refresh();
    try {
      await performToggle();
    } finally {
      transportPending = false;
      for (const id of ["play-toggle", "full-play"])
        el(id).removeAttribute("aria-busy");
      refresh();
    }
  }
  for (const id of ["play-toggle", "full-play"])
    el(id).addEventListener("click", toggle);
  for (const id of ["seek", "full-seek"])
    el(id).addEventListener("input", (event) => {
      if (Number.isFinite(audio.duration))
        audio.currentTime =
          (audio.duration * Number(event.target.value)) / 1000;
    });
  for (const name of [
    "timeupdate",
    "durationchange",
    "loadedmetadata",
    "play",
    "pause",
    "ended",
    "emptied",
  ])
    audio.addEventListener(name, refresh);
  for (const [name, label] of [
    ["waiting", "Buffering…"],
    ["playing", "Playing"],
    ["pause", "Paused"],
    ["seeking", "Seeking…"],
    ["ended", "Track finished"],
    ["error", "This track cannot play. Try another track."],
  ])
    audio.addEventListener(name, () => {
      el("playback-state").textContent = queueStepPending
        ? "Changing song…"
        : label;
    });
  let playerTransition = null;
  let playerFallback = null;
  let transitionTarget = null;
  const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)");
  const artOverlay = document.createElement("img");
  artOverlay.id = "player-art-transition";
  artOverlay.alt = "";
  artOverlay.setAttribute("aria-hidden", "true");
  artOverlay.setAttribute("popover", "manual");
  document.body.append(artOverlay);
  function cancelPlayerMotion() {
    const animation = playerTransition;
    playerTransition = null;
    animation?.cancel();
    playerFallback?.cancel();
    if (transitionTarget) transitionTarget.style.visibility = "";
    transitionTarget = null;
    if (artOverlay.showPopover && artOverlay.matches(":popover-open"))
      artOverlay.hidePopover();
    dialog.dataset.transitioning = "false";
  }
  function showPlayer(open) {
    cancelPlayerMotion();
    const source = dialog.open ? el("full-art") : el("cover");
    const from = source.getBoundingClientRect();
    if (open && !dialog.open) {
      dialog.showModal();
      dialog.scrollTop = 0;
      dialog.dataset.scrolled = "false";
    } else if (!open && dialog.open) dialog.close();
    if (reduceMotion.matches || document.body.dataset.input === "keyboard")
      return;
    const target = open ? el("full-art") : el("cover");
    const to = target.getBoundingClientRect();
    if (
      !artOverlay.showPopover ||
      !source.complete ||
      !source.naturalWidth ||
      !from.width ||
      !to.width ||
      from.bottom <= 0
    ) {
      if (open) {
        playerFallback = dialog.querySelector(".full-layout").animate(
          [
            { opacity: 0, transform: "translateY(6px)" },
            { opacity: 1, transform: "none" },
          ],
          { duration: 200, easing: "cubic-bezier(0.23,1,0.32,1)" },
        );
        playerFallback.finished.catch(() => {});
      }
      return;
    }
    // Fixed-size isolated artwork layer: only transform changes per frame.
    // The dialog opens/closes immediately; audio and input remain independent.
    artOverlay.src = source.currentSrc || source.src;
    artOverlay.style.width = `${to.width}px`;
    artOverlay.style.height = `${to.height}px`;
    artOverlay.showPopover();
    transitionTarget = target;
    target.style.visibility = "hidden";
    dialog.dataset.transitioning = "true";
    const animation = artOverlay.animate(
      [
        {
          transform: `translate(${from.x}px,${from.y}px) scale(${from.width / to.width},${from.height / to.height})`,
        },
        { transform: `translate(${to.x}px,${to.y}px) scale(1)` },
      ],
      { duration: 280, easing: "cubic-bezier(0.32,0.72,0,1)", fill: "both" },
    );
    playerTransition = animation;
    animation.finished
      .catch(() => {})
      .finally(() => {
        if (playerTransition === animation) cancelPlayerMotion();
      });
  }
  dialog.addEventListener("cancel", cancelPlayerMotion);
  dialog.addEventListener(
    "scroll",
    () => {
      dialog.dataset.scrolled = String(dialog.scrollTop > 0);
    },
    { passive: true },
  );
  window.addEventListener("resonance:cancel-motion", cancelPlayerMotion);
  window.addEventListener("resize", cancelPlayerMotion);
  window.visualViewport?.addEventListener("resize", cancelPlayerMotion);
  window.addEventListener("resonance:track", cancelPlayerMotion);
  reduceMotion.addEventListener("change", () => {
    if (reduceMotion.matches) cancelPlayerMotion();
  });
  el("open-player").addEventListener("click", () => {
    showPlayer(true);
    loadQueueRail();
    if (displayTrack()) loadTrackActions(displayTrack());
  });
  el("close-player").addEventListener("click", () => showPlayer(false));
  for (const id of ["open-queue", "full-queue"])
    el(id).addEventListener("click", () => {
      cancelPlayerMotion();
      dialog.close();
      window.resonanceBrowse.selectView("queue");
    });
  el("full-next").addEventListener("click", () => el("player-next").click());
  el("full-previous").addEventListener("click", () =>
    el("player-previous").click(),
  );
  el("full-art").addEventListener("error", () => {
    if (el("full-art").dataset.missing !== "true")
      window.resonanceUI.setArtwork(el("full-art"), null);
  });
  el("full-favorite").addEventListener("click", async () => {
    const target = displayTrack();
    if (!target || favoritePending) return;
    favoritePending = true;
    el("full-favorite").disabled = true;
    el("full-favorite").setAttribute("aria-busy", "true");
    try {
      await window.resonanceUser.toggleFavorite(target);
    } catch {
      if (displayTrack()?.id === target.id)
        el("playback-state").textContent =
          "Favorite could not be saved. Try again.";
    } finally {
      favoritePending = false;
      el("full-favorite").removeAttribute("aria-busy");
      renderTrackActions();
    }
  });
  el("full-playlist").addEventListener("click", () => {
    if (displayTrack()) window.resonanceUser.choosePlaylist(displayTrack());
  });
  el("full-album").addEventListener("click", async () => {
    const target = displayTrack();
    if (!target?.album_id) return;
    try {
      const response = await fetch(`/api/v1/albums/${target.album_id}`, {
        cache: "no-store",
      });
      if (!response.ok) throw Error();
      const album = await response.json();
      if (displayTrack()?.id !== target.id) return;
      dialog.close();
      window.resonanceBrowse.openGroup("albums", album);
    } catch {
      if (displayTrack()?.id === target.id)
        el("playback-state").textContent = "Album is unavailable. Try again.";
    }
  });
  function renderTrackActions() {
    const target = displayTrack();
    const favorite = target && window.resonanceUser.isFavorite(target.id);
    window.resonanceUI.button(
      el("full-favorite"),
      favorite ? "heart-fill" : "heart",
      target
        ? `${favorite ? "Remove favorite" : "Favorite"}: ${target.title || "Untitled track"}`
        : "Favorite",
      true,
    );
    el("full-favorite").setAttribute("aria-pressed", String(!!favorite));
    el("full-favorite").disabled =
      favoritePending ||
      !target ||
      !window.resonanceUser.favoriteStatus(target.id).known;
    el("full-playlist").disabled = !target;
    el("full-album").hidden = !target?.album_id;
    el("full-album").textContent = target?.album_title || "View album";
    el("full-album-text").hidden = !!target?.album_id || !target?.album_title;
    el("full-album-text").textContent = target?.album_title || "";
  }
  function loadTrackActions(target) {
    renderTrackActions();
    window.resonanceUser.loadFavorites([target.id]).catch(() => {
      if (displayTrack()?.id === target.id)
        el("playback-state").textContent =
          "Favorite status is unavailable. Reopen the player to retry.";
    });
  }
  window.addEventListener("resonance:track", (event) => {
    track = event.detail;
    preparedTrack = null;
    preparedSelection = null;
    prepareGeneration++;
    el("full-title").textContent = track.title || "Untitled track";
    el("full-artist").textContent = track.artist_credit || "Unknown artist";
    window.resonanceUI.setArtwork(
      el("full-art"),
      track.artwork_url,
      window.resonanceUI.artworkKey(track),
    );
    loadTrackActions(track);
    el("playback-state").textContent = "Loading…";
    refresh();
    if ("mediaSession" in navigator && "MediaMetadata" in window)
      navigator.mediaSession.metadata = new MediaMetadata({
        title: track.title || "Untitled track",
        artist: track.artist_credit || "Unknown artist",
        album: track.album_title || "",
        artwork: track.artwork_url ? [{ src: track.artwork_url }] : [],
      });
  });
  let railGeneration = 0;
  async function loadQueueRail() {
    const generation = ++railGeneration;
    el("full-queue-status").textContent = "Loading your queue…";
    try {
      await window.resonanceUser.readQueue();
    } catch {
      if (generation === railGeneration)
        el("full-queue-status").textContent =
          "Queue unavailable. Try Refresh queue.";
    }
  }
  let railSignature = "";
  function renderQueueRail(snapshot) {
    const list = el("full-queue-items");
    const selected = snapshot.items.findIndex(
      (item) => item.id === snapshot.current_item_id,
    );
    const start = Math.max(0, selected);
    const modes = window.resonancePlaybackModes.get();
    const availableTracks = new Set(
      snapshot.items
        .filter((item) => item.available)
        .map((item) => item.track_id),
    );
    const atBoundary =
      selected >= 0 &&
      !snapshot.items.slice(selected + 1).some((item) => item.available);
    const cycling =
      window.resonancePlaybackModes.advanceOptions("next").repeat === "all" &&
      modes.repeat !== "one" &&
      atBoundary &&
      !snapshot.context?.more &&
      availableTracks.size > 0;
    const shown = snapshot.items.slice(start, start + 20);
    if (
      cycling &&
      !snapshot.context &&
      !modes.shuffle &&
      snapshot.items.length > 1
    )
      shown.push(
        ...snapshot.items
          .filter((item) => item.available)
          .slice(0, 20 - shown.length)
          .map((item) => ({ ...item, next_round: true })),
      );
    el("full-queue-status").textContent = snapshot.items.length
      ? window.resonanceListening.get().changing
        ? "Changing song…"
        : snapshot.context?.more &&
            atBoundary &&
            window.resonanceListening.get().followsQueue
          ? `More songs from ${snapshot.context.name} follow automatically.`
          : cycling && window.resonanceListening.get().followsQueue
            ? snapshot.context
              ? modes.shuffle
                ? `A fresh shuffled round from ${snapshot.context.name} starts after this song.`
                : `A new round from ${snapshot.context.name} starts after this song.`
              : availableTracks.size === 1
                ? "This song repeats after it finishes."
                : modes.shuffle
                  ? "A fresh shuffled round starts after this song."
                  : "The next round starts after this song."
            : window.resonanceListening.get().explanation
      : "Add a song to decide what plays next.";
    const signature = JSON.stringify([
      snapshot.revision,
      snapshot.current_item_id,
      snapshot.selection_token,
      window.resonanceListening.get().mode,
      modes.continuous,
      modes.shuffle,
      modes.repeat,
      shown.map((item) => [
        item.id,
        item.position,
        item.title,
        item.artist_credit,
        item.track_id,
        item.available,
        item.last_skip_code,
        item.next_round,
      ]),
    ]);
    if (signature === railSignature) return;
    railSignature = signature;
    list.replaceChildren();
    for (const item of shown) {
      const row = document.createElement("li");
      const img = window.resonanceBrowse.artwork(null);
      row.append(img);
      window.resonanceBrowse.watchArtwork(img, item.track_id);
      const copy = window.resonanceBrowse.node("div", "", "track-copy");
      copy.append(
        window.resonanceBrowse.node("strong", item.title || "Untitled track"),
        window.resonanceBrowse.node(
          "span",
          item.artist_credit || "Unknown artist",
          "item-subtitle",
        ),
      );
      if (item.next_round)
        copy.append(
          window.resonanceBrowse.node("span", "Next round", "queue-selection"),
        );
      else if (item.id === snapshot.current_item_id)
        copy.append(
          window.resonanceBrowse.node(
            "span",
            window.resonanceListening.get().followsQueue
              ? "Now Playing"
              : "Saved queue position",
            "queue-selection",
          ),
        );
      if (!item.available)
        copy.append(
          window.resonanceBrowse.node("span", "Unavailable", "queue-selection"),
        );
      else if (item.last_skip_code)
        copy.append(
          window.resonanceBrowse.node(
            "span",
            "Previously skipped",
            "queue-selection",
          ),
        );
      row.append(copy);
      const more = window.resonanceBrowse.action("More", () =>
        window.resonanceUser.playerQueueActions(snapshot, item.id, more),
      );
      window.resonanceUI.button(
        more,
        "dots-three",
        `More actions for ${item.title || "Untitled track"} by ${item.artist_credit || "Unknown artist"} · position ${item.position + 1}${item.next_round ? " · next round" : ""}`,
        true,
      );
      row.append(more);
      list.append(row);
    }
  }
  el("full-queue-refresh").addEventListener("click", loadQueueRail);
  el("full-queue-all").addEventListener("click", () => {
    cancelPlayerMotion();
    dialog.close();
    window.resonanceBrowse.selectView("queue");
  });
  window.addEventListener("resonance:queue", (event) => {
    lastQueueSnapshot = event.detail;
    refresh();
    if (dialog.open) renderQueueRail(event.detail);
    if (!track) prepareSelection(event.detail);
  });
  let preparedArtwork = null;
  function readPreparedArtwork(item) {
    const key = JSON.stringify([
      item.track_id,
      item.title,
      item.artist_credit,
      item.available,
    ]);
    if (
      preparedArtwork?.key === key &&
      (preparedArtwork.expires === null ||
        preparedArtwork.expires > performance.now())
    )
      return preparedArtwork.promise;
    const entry = { key, expires: null, promise: null };
    entry.promise = fetch(`/api/v1/tracks/${item.track_id}`, {
      cache: "no-store",
    })
      .then(async (response) => {
        if (!response.ok) throw Error("Artwork metadata unavailable");
        const detail = await response.json();
        entry.expires = performance.now() + 30000;
        return { ...detail, id: item.track_id };
      })
      .catch((error) => {
        if (preparedArtwork === entry) preparedArtwork = null;
        throw error;
      });
    preparedArtwork = entry;
    return entry.promise;
  }
  async function prepareSelection(snapshot) {
    const generation = ++prepareGeneration;
    const item = snapshot.items.find(
      (item) => item.id === snapshot.current_item_id,
    );
    preparedSelection =
      snapshot.selection_state === "selected" && item?.available
        ? { itemID: item.id, token: snapshot.selection_token }
        : null;
    const previousTrackID = preparedTrack?.id;
    preparedTrack = preparedSelection
      ? preparedTrack?.id === item.track_id
        ? preparedTrack
        : {
            id: item.track_id,
            title: item.title,
            artist_credit: item.artist_credit,
          }
      : null;
    if (previousTrackID !== preparedTrack?.id) {
      const key = preparedTrack
        ? window.resonanceUI.artworkKey(preparedTrack)
        : "";
      window.resonanceUI.setArtwork(el("cover"), null, key);
      window.resonanceUI.setArtwork(el("full-art"), null, key);
    }
    renderTrackActions();
    if (preparedSelection) {
      loadTrackActions(preparedTrack);
      el("now-title").textContent = item.title || "Untitled track";
      el("now-credit").textContent = item.artist_credit || "Unknown artist";
      el("full-title").textContent = item.title || "Untitled track";
      el("full-artist").textContent = item.artist_credit || "Unknown artist";
      el("playback-state").textContent = "Ready to play your selection.";
      refresh();
      try {
        const detail = await readPreparedArtwork(item);
        if (generation === prepareGeneration && !track) {
          preparedTrack = detail;
          renderTrackActions();
          window.resonanceUI.setArtwork(
            el("cover"),
            detail.artwork_url,
            window.resonanceUI.artworkKey(detail),
          );
          window.resonanceUI.setArtwork(
            el("full-art"),
            detail.artwork_url,
            window.resonanceUI.artworkKey(detail),
          );
        }
      } catch {
        /* A prepared selection never starts playback or listening history. */
      }
    } else {
      el("now-title").textContent = "Make room for a song.";
      el("now-credit").textContent = "Choose something from your library";
      el("full-title").textContent = "Choose a track";
      el("full-artist").textContent = "";
      el("full-album").hidden = true;
      window.resonanceUI.setArtwork(el("cover"), null, "");
      window.resonanceUI.setArtwork(el("full-art"), null, "");
      el("playback-state").textContent = "Ready when you are.";
      refresh();
    }
  }
  new MutationObserver(() => {
    if (dialog.open && !el("player-error").hidden)
      el("playback-state").textContent = el("player-error").textContent;
  }).observe(el("player-error"), {
    childList: true,
    characterData: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["hidden"],
  });
  window.addEventListener("resonance:favorite", (event) => {
    if (displayTrack()?.id === event.detail.id) renderTrackActions();
  });
  window.resonanceListening.subscribe(() => {
    refresh();
    if (dialog.open && lastQueueSnapshot) renderQueueRail(lastQueueSnapshot);
  });
  window.addEventListener("resonance:queue-step", (event) => {
    queueStepPending = event.detail.pending;
    el("playback-state").textContent = queueStepPending
      ? "Changing song…"
      : audio.error
        ? "This track cannot play. Try another track."
        : audio.paused
          ? audio.ended
            ? "Track finished"
            : "Paused"
          : audio.readyState < 3
            ? "Loading…"
            : "Playing";
    refresh();
  });
  window.addEventListener("resonance:modes", refresh);
  refresh();
  window.resonanceUser.readQueue().catch(() => {});
  if ("mediaSession" in navigator)
    for (const [name, handler] of Object.entries({
      play: () => {
        if (audio.paused) toggle().catch(() => {});
      },
      pause: () => audio.pause(),
      previoustrack: () => el("player-previous").click(),
      nexttrack: () => el("player-next").click(),
      seekto: (details) => {
        if (
          Number.isFinite(audio.duration) &&
          Number.isFinite(details.seekTime)
        )
          audio.currentTime = Math.max(
            0,
            Math.min(audio.duration, details.seekTime),
          );
      },
    })) {
      try {
        navigator.mediaSession.setActionHandler(name, handler);
      } catch {
        /* Capability varies by browser. */
      }
    }
})();
