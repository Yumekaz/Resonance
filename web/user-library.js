(() => {
  "use strict";
  const userItems = document.querySelector("#items");
  const userStatus = document.querySelector("#status");
  const userTitle = document.querySelector("#view-title");
  const userMore = document.querySelector("#more");
  const userAudio = document.querySelector("#audio");
  const userError = document.querySelector("#player-error");
  const queueControls = document.querySelector("#queue-controls");
  const playlistCreate = document.querySelector("#playlist-create");
  const newPlaylist = document.querySelector("#new-playlist");
  const createPlaylistDialog = document.querySelector(
    "#create-playlist-dialog",
  );
  const createPlaylistError = document.querySelector("#create-playlist-error");
  let activeView = null;
  let queue = null;
  let queueWrites = Promise.resolve(),
    playIntent = 0;
  let currentPlaylist = null;
  let viewCursor = null;
  let favoriteLoadGeneration = 0;
  let userViewGeneration = 0;
  let userViewAbort = new AbortController();
  let occurrenceLimit = 100;
  let queuePagePinned = false;
  let userPageIndex = 0,
    userPageCursors = [null];
  const userPrevious = document.querySelector("#previous-page");

  function uiNode(tag, value, className) {
    const element = document.createElement(tag);
    element.textContent = value;
    if (className) element.className = className;
    return element;
  }
  function uiAction(label, fn) {
    const button = uiNode("button", label);
    button.type = "button";
    button.addEventListener("click", async (event) => {
      event.stopPropagation();
      if (button.disabled || button.getAttribute("aria-busy") === "true")
        return;
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      try {
        await fn(event.currentTarget);
      } catch (error) {
        userFailure(error);
      } finally {
        button.disabled = false;
        button.removeAttribute("aria-busy");
      }
    });
    return button;
  }
  function userFailure(error) {
    if (error?.name === "AbortError" || error?.code === "view_changed") return;
    userStatus.textContent =
      error?.code === "stale_selection"
        ? "Queue selection changed in another tab. Refresh the queue."
        : error?.code === "stale_version"
          ? "This list changed in another tab. Refresh and try again."
          : "The change was not saved. Check the server and try again.";
    const dialog =
      document.querySelector(".context-menu") ||
      document.querySelector("#action-dialog");
    if (document.querySelector("#now-playing")?.open)
      document.querySelector("#playback-state").textContent =
        userStatus.textContent;
    if (dialog?.open || dialog?.classList.contains("context-menu")) {
      let message = dialog.querySelector(".dialog-status");
      if (!message) {
        message = uiNode("p", "", "dialog-status");
        message.setAttribute("role", "status");
        dialog.append(message);
      }
      message.textContent = userStatus.textContent;
    }
  }
  const userAPI = window.resonanceAPI.read;
  const userWrite = window.resonanceAPI.write;
  const favorites = window.resonanceCreateFavorites({
    read: async (ids) =>
      (
        await userAPI("/api/v1/favorites/lookup", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ track_ids: ids }),
        })
      ).track_ids,
    set: (id, present) =>
      userAPI(`/api/v1/favorites/tracks/${id}`, {
        method: present ? "PUT" : "DELETE",
      }),
    changed: (detail) =>
      window.dispatchEvent(new CustomEvent("resonance:favorite", { detail })),
  });
  window.addEventListener("resonance:favorite", (event) => {
    for (const row of userItems.querySelectorAll("[data-track-id]")) {
      if (row.dataset.trackId !== event.detail.id) continue;
      const button = row.querySelector(".favorite-button");
      if (!button) continue;
      window.resonanceUI.button(
        button,
        event.detail.present ? "heart-fill" : "heart",
        button.getAttribute("aria-label"),
        true,
      );
      button.setAttribute("aria-pressed", String(event.detail.present));
      button.disabled = false;
      button.removeAttribute("aria-busy");
    }
  });
  const listeningSession = window.resonanceCreateListeningSession({
    audio: userAudio,
    error: userError,
    failure: userFailure,
    getQueue: () => queue,
    refreshQueue,
    selectedQueuePlayback,
    onQueueChanged: async () => {
      if (activeView === "queue") await loadUserView(true);
    },
  });
  const { setPlayback, accrue, sendReport, retryPending } = listeningSession;
  async function refreshQueue() {
    queue = await userAPI("/api/v1/queue");
    window.dispatchEvent(new CustomEvent("resonance:queue", { detail: queue }));
    return queue;
  }
  async function refreshFavorites() {
    const ids = [...userItems.querySelectorAll("[data-track-id]")].map(
      (row) => row.dataset.trackId,
    );
    if (ids.length) await favorites.load(ids);
  }
  function setControls(mode) {
    queueControls.hidden = mode !== "queue" || !queue?.items.length;
    newPlaylist.hidden = mode !== "playlists" || !!currentPlaylist;
  }
  function onBrowseView(mode) {
    userViewAbort.abort();
    userViewAbort = new AbortController();
    userViewGeneration++;
    occurrenceLimit = 100;
    userPageIndex = 0;
    userPageCursors = [null];
    activeView = null;
    currentPlaylist = null;
    setControls(mode);
  }
  function selectUserView(mode, playlistID = null) {
    userViewAbort.abort();
    userViewAbort = new AbortController();
    userViewGeneration++;
    occurrenceLimit = 100;
    queuePagePinned = false;
    userPageIndex = 0;
    userPageCursors = [null];
    activeView = mode;
    document.querySelector("#playlist-back").hidden = !playlistID;
    currentPlaylist = playlistID;
    viewCursor = null;
    userTitle.textContent =
      mode === "queue" ? "Up Next" : mode[0].toUpperCase() + mode.slice(1);
    userItems.replaceChildren();
    userMore.hidden = true;
    setControls(mode);
    window.resonanceBrowse.syncNavigation(mode);
    const navigationGeneration = userViewGeneration;
    const previousFocus = document.activeElement;
    loadUserView()
      .then(() => {
        if (
          navigationGeneration === userViewGeneration &&
          (document.activeElement === previousFocus ||
            document.activeElement === document.body)
        )
          userTitle.focus({ preventScroll: true });
      })
      .catch(userFailure);
  }
  function addQueue(track, placement) {
    const intent = placement === "now" ? ++playIntent : null;
    const operation = queueWrites
      .catch(() => {})
      .then(() => performAddQueue(track, placement, intent));
    queueWrites = operation;
    return operation;
  }
  async function performAddQueue(track, placement, intent) {
    if (!track?.id) return;
    const q = await refreshQueue();
    const change = await userWrite("POST", "/api/v1/queue/items", {
      track_id: track.id,
      placement,
      expected_version: q.revision,
    });
    await refreshQueue();
    if (placement === "now" && intent === playIntent && change?.item_id) {
      window.resonanceBrowse.playTrack(track, {
        itemID: change.item_id,
        token: change.selection_token,
      });
    }
    if (activeView === "queue") await loadUserView(true);
  }
  async function selectQueueItem(snapshot, itemID) {
    queuePagePinned = false;
    const change = await userWrite("POST", "/api/v1/queue/select", {
      item_id: itemID,
      expected_version: snapshot.revision,
    });
    await selectedQueuePlayback({
      itemID: change.current_item_id,
      token: change.selection_token,
    });
    if (activeView === "queue") await loadUserView(true);
  }
  function addCollection(entries, placement) {
    const intent = placement === "now" ? ++playIntent : null;
    const operation = queueWrites
      .catch(() => {})
      .then(() => performAddCollection(entries, placement, intent));
    queueWrites = operation;
    return operation;
  }
  async function performAddCollection(entries, placement, intent) {
    let available = entries.filter((track) => track.available);
    if (placement === "now" && window.resonancePlaybackModes.get().shuffle)
      available = window.resonancePlaybackModes.shuffled(available);
    if (!available.length) {
      userStatus.textContent = "No available songs in this collection.";
      return;
    }
    const q = await refreshQueue();
    if (available.length + q.items.length > 1000) {
      userStatus.textContent =
        "Your queue holds up to 1,000 songs. Remove some songs or add a smaller collection.";
      return;
    }
    const change = await userWrite("POST", "/api/v1/queue/collection", {
      track_ids: available.map((track) => track.track_id || track.id),
      placement,
      expected_version: q.revision,
    });
    await refreshQueue();
    if (placement === "now" && intent === playIntent)
      await selectedQueuePlayback({
        itemID: change.current_item_id,
        token: change.selection_token,
      });
    userStatus.textContent = `${available.length} ${available.length === 1 ? "song" : "songs"} added to your queue${available.length < entries.length ? ` · ${entries.length - available.length} unavailable skipped` : ""}.`;
    if (activeView === "queue") await loadUserView(true);
  }
  function playContext(entries, selectedID) {
    const intent = ++playIntent;
    const operation = queueWrites
      .catch(() => {})
      .then(async () => {
        const modes = window.resonancePlaybackModes;
        let tracks = entries.filter((track) => track.available);
        const originalTracks = [...tracks];
        let start = tracks.findIndex((track) => track.id === selectedID);
        if (start < 0) throw { code: "track_unavailable" };
        if (modes.get().shuffle) {
          const selected = tracks[start];
          tracks = [
            selected,
            ...modes.shuffled(tracks.filter((_, index) => index !== start)),
          ];
          start = 0;
        }
        const q = await refreshQueue();
        const change = await userWrite("POST", "/api/v1/queue/collection", {
          track_ids: tracks.map((track) => track.id),
          placement: "replace",
          start_index: start,
          expected_version: q.revision,
        });
        const updated = await refreshQueue();
        if (
          modes.get().shuffle &&
          updated.current_item_id === change.current_item_id &&
          updated.selection_token === change.selection_token
        ) {
          const occurrences = new Map();
          for (const item of updated.items) {
            if (!occurrences.has(item.track_id))
              occurrences.set(item.track_id, []);
            occurrences.get(item.track_id).push(item.id);
          }
          modes.setShuffle(
            true,
            originalTracks
              .map((track) => occurrences.get(track.id)?.shift())
              .filter(Boolean),
          );
        }
        if (intent === playIntent)
          await selectedQueuePlayback({
            itemID: change.current_item_id,
            token: change.selection_token,
          });
        userStatus.textContent = `Playing from this view · ${tracks.length} ${tracks.length === 1 ? "song" : "songs"} in Up Next.`;
      });
    queueWrites = operation;
    return operation;
  }
  async function toggleFavorite(track) {
    return favorites.toggle(track.id);
  }
  let actionDialogGeneration = 0;
  function openActionDialog(heading) {
    window.resonanceMenus.close();
    const generation = ++actionDialogGeneration;
    const dialog = document.querySelector("#action-dialog");
    dialog.querySelector(".dialog-status")?.remove();
    document.querySelector("#dialog-title").textContent = heading;
    const content = document.querySelector("#dialog-content");
    content.replaceChildren();
    if (!dialog.open) dialog.showModal();
    else
      dialog
        .querySelector('button[aria-label="Close dialog"]')
        ?.focus({ preventScroll: true });
    const current = () => generation === actionDialogGeneration && dialog.open;
    return {
      dialog,
      content,
      current,
      close: () => {
        if (current()) dialog.close();
      },
    };
  }
  async function choosePlaylist(track) {
    return window.resonancePlaylistDestination(track);
  }
  function trackContext(track) {
    return `${track.title || "Untitled track"} by ${track.artist_credit || "Unknown artist"}${track.album_title ? " · " + track.album_title : ""}`;
  }
  function decorateTrackRow(row, track) {
    const controls = uiNode("span", "", "item-actions");
    const favorite = uiAction(
      favorites.get(track.id).present ? "Saved" : "Favorite",
      async () => {
        const added = await toggleFavorite(track);
        window.resonanceUI.button(
          favorite,
          added ? "heart-fill" : "heart",
          "Favorite " + trackContext(track),
          true,
        );
        favorite.setAttribute("aria-pressed", String(added));
      },
    );
    favorite.setAttribute(
      "aria-pressed",
      String(favorites.get(track.id).present),
    );
    favorite.setAttribute("aria-label", "Favorite " + trackContext(track));
    favorite.classList.add("favorite-button");
    favorite.disabled = !favorites.get(track.id).known;
    if (favorite.disabled) favorite.setAttribute("aria-busy", "true");
    favorites.ensure(track.id).catch(() => {
      if (!favorite.isConnected) return;
      favorite.removeAttribute("aria-busy");
      favorite.disabled = false;
    });

    window.resonanceUI.button(
      favorite,
      favorites.get(track.id).present ? "heart-fill" : "heart",
      "Favorite " + trackContext(track),
      true,
    );
    controls.append(
      favorite,
      uiAction("More", (anchor) => {
        const { close, content } = window.resonanceMenus.open(
          anchor,
          track.title || "Track actions",
        );
        for (const [text, placement] of [
          ["Play now", "now"],
          ["Play next", "next"],
          ["Add to queue", "end"],
        ]) {
          const b = uiAction(text, async () => {
            const feedbackGeneration = userViewGeneration;
            await addQueue(track, placement);
            close();
            if (feedbackGeneration === userViewGeneration)
              userStatus.textContent = "Added to queue.";
          });
          if (placement === "now") b.disabled = !track.available;
          content.append(b);
        }
        const solo = uiAction("Play this song only", () => {
          close();
          window.resonanceBrowse.playTrack(track);
        });
        solo.disabled = !track.available;
        content.append(solo);
        content.append(
          uiAction("Add to playlist", () => choosePlaylist(track)),
        );
        content.append(
          uiAction(
            favorites.get(track.id).present ? "Remove favorite" : "Favorite",
            async () => {
              await toggleFavorite(track);
              close();
              userStatus.textContent = "Favorite updated.";
            },
          ),
        );
        if (track.artist_id)
          content.append(
            uiAction("View artist", async () => {
              close();
              window.resonanceBrowse.openGroup(
                "artists",
                await userAPI(`/api/v1/artists/${track.artist_id}`),
              );
            }),
          );
        if (track.album_id)
          content.append(
            uiAction("View album", async () => {
              close();
              window.resonanceBrowse.openGroup(
                "albums",
                await userAPI(`/api/v1/albums/${track.album_id}`),
              );
            }),
          );
      }),
    );
    for (const button of controls.querySelectorAll("button"))
      if (button.textContent === "More")
        window.resonanceUI.button(
          button,
          "dots-three",
          "More actions for " + trackContext(track),
          true,
        );
    row.append(controls);
  }
  async function viewAPI(path) {
    const generation = userViewGeneration;
    const result = await userAPI(path, { signal: userViewAbort.signal });
    if (generation !== userViewGeneration) throw { code: "view_changed" };
    return result;
  }
  async function selectedQueuePlayback(expected = null) {
    const q = await refreshQueue();
    if (
      expected &&
      (q.selection_state !== "selected" ||
        q.current_item_id !== expected.itemID ||
        q.selection_token !== expected.token)
    )
      return;
    if (q.selection_state !== "selected" || !q.current_item_id) return;
    const item = q.items.find((value) => value.id === q.current_item_id);
    if (!item?.available) {
      userStatus.textContent = "Selected Track is unavailable.";
      return;
    }
    const track = await userAPI(`/api/v1/tracks/${item.track_id}`);
    window.resonanceBrowse.playTrack(track, {
      itemID: item.id,
      token: q.selection_token,
    });
  }
  let queueStepPending = false;
  async function advanceQueue(direction, failureCode = null) {
    if (queueStepPending || window.resonanceListening.get().changing) return;
    queueStepPending = true;
    window.dispatchEvent(
      new CustomEvent("resonance:queue-step", { detail: { pending: true } }),
    );
    try {
      await performAdvanceQueue(direction, failureCode);
    } finally {
      queueStepPending = false;
      window.dispatchEvent(
        new CustomEvent("resonance:queue-step", { detail: { pending: false } }),
      );
    }
  }
  async function performAdvanceQueue(direction, failureCode = null) {
    queuePagePinned = false;
    const q = queue || (await refreshQueue());
    if (!q.items.length) return;
    const playback = listeningSession.current();
    const projected = window.resonanceListening.get();
    const prepared = playback?.track
      ? null
      : { itemID: q.current_item_id, token: q.selection_token };
    const controls = window.resonanceListening.controls(
      q,
      projected.playback,
      prepared,
      userAudio.currentTime,
      window.resonancePlaybackModes.advanceOptions(direction).repeat,
    );
    if (!controls[direction]) {
      if (projected.mode === "detached") throw { code: "stale_selection" };
      userStatus.textContent =
        direction === "next"
          ? "No next song in Up Next."
          : "No previous song in Up Next.";
      return;
    }
    if (
      direction === "previous" &&
      projected.followsQueue &&
      listeningSession.current()?.selection &&
      userAudio.currentTime > 3
    ) {
      userAudio.currentTime = 0;
      return;
    }
    const selection = projected.followsQueue ? playback?.selection : null;
    const currentItemID = selection ? selection.itemID : q.current_item_id;
    const selectionToken = selection ? selection.token : q.selection_token;
    const change = await userWrite("POST", "/api/v1/queue/advance", {
      direction,
      ...window.resonancePlaybackModes.advanceOptions(direction),
      expected_version: q.revision,
      expected_current_item_id: currentItemID,
      selection_token: selectionToken,
      ...(failureCode ? { failure_code: failureCode } : {}),
    });
    await refreshQueue();
    if (activeView === "queue") await loadUserView(true);
    const selectionUnchanged =
      change?.selection_state === "selected" &&
      change.current_item_id === currentItemID &&
      change.selection_token === selectionToken;
    if (!selectionUnchanged) {
      accrue();
      userAudio.pause();
      if (listeningSession.current()?.session) await sendReport("stopped");
    }
    if (
      change?.selection_state === "selected" &&
      change.current_item_id &&
      change.selection_token &&
      !selectionUnchanged
    )
      await selectedQueuePlayback({
        itemID: change.current_item_id,
        token: change.selection_token,
      });
    else if (change?.selection_state !== "selected") userAudio.pause();
  }
  async function reorderQueue(q, index, delta) {
    if (index + delta < 0 || index + delta >= q.items.length) return;
    const before = window.resonanceRows.capture(userItems);
    const order = q.items.map((item) => item.id);
    [order[index], order[index + delta]] = [order[index + delta], order[index]];
    await userWrite("PUT", "/api/v1/queue/order", {
      item_ids: order,
      expected_version: q.revision,
    });
    occurrenceLimit = (Math.floor((index + delta) / 100) + 1) * 100;
    queuePagePinned = true;
    await loadUserView(true);
    window.resonanceRows.settle(userItems, before, q.items[index].id);
    userStatus.textContent = `${q.items[index].title || "Song"} moved to position ${index + delta + 1}.`;
  }
  function occurrenceRow(item, index, playAction = null) {
    const row = uiNode("li", "", "track-row");
    row.dataset.itemId = item.id;
    row.append(
      uiNode("span", String(index + 1), "track-number"),
      window.resonanceBrowse.artwork(null),
    );
    const copy = uiNode("div", "", "track-copy");
    const play = uiAction(
      item.title || "Untitled track",
      playAction ||
        (async () =>
          window.resonanceBrowse.play(
            await userAPI(`/api/v1/tracks/${item.track_id}`),
          )),
    );
    play.className = "item-title";
    play.disabled = !item.available;
    copy.append(
      play,
      uiNode("span", item.artist_credit || "Unknown artist", "item-subtitle"),
    );
    row.append(copy);
    window.resonanceBrowse.watchArtwork(
      row.querySelector("img"),
      item.track_id,
    );
    if (!item.available) row.append(uiNode("span", "Unavailable", "badge"));
    return row;
  }
  function occurrenceMenu(row, item, actions) {
    const holder = uiNode("span", "", "item-actions");
    holder.append(
      uiAction("More", (anchor) => {
        const { close, content } = window.resonanceMenus.open(
          anchor,
          item.title || "Track actions",
        );
        for (const [label, fn] of actions) {
          const b = uiAction(label, async () => {
            const focusGeneration = userViewGeneration;
            await fn();
            const completed = close();
            if (focusGeneration !== userViewGeneration) return;
            if (
              completed ||
              (!window.resonanceMenus.hasOpen() &&
                document.activeElement === document.body)
            ) {
              const rows = [
                ...userItems.querySelectorAll(".track-row[data-item-id]"),
              ];
              const same = rows.find((row) => row.dataset.itemId === item.id);
              const nearest =
                rows.find(
                  (row) =>
                    Number(row.querySelector(".track-number")?.textContent) >=
                    item.position + 1,
                ) || rows.at(-1);
              (
                (same || nearest)?.querySelector(".item-actions button") ||
                userTitle
              ).focus({ preventScroll: true });
            }
          });
          if (label === "Play now") b.disabled = !item.available;
          content.append(b);
        }
      }),
    );
    for (const button of holder.querySelectorAll("button"))
      window.resonanceUI.button(
        button,
        "dots-three",
        "More actions for " +
          trackContext(item) +
          ` · position ${item.position + 1}`,
        true,
      );
    row.append(holder);
  }
  async function renderQueue() {
    const q = await viewAPI("/api/v1/queue");
    queue = q;
    window.dispatchEvent(new CustomEvent("resonance:queue", { detail: q }));
    userItems.replaceChildren();
    userMore.hidden = true;
    if (!queuePagePinned) {
      const selectedIndex = q.items.findIndex(
        (item) => item.id === q.current_item_id,
      );
      occurrenceLimit = Math.max(0, selectedIndex) + 100;
    }
    occurrenceLimit = Math.min(
      occurrenceLimit,
      Math.max(100, q.items.length + 99),
    );
    userPrevious.hidden = occurrenceLimit === 100;
    queueControls.hidden = !q.items.length;
    for (const control of queueControls.querySelectorAll("button"))
      control.disabled = !q.items.length;
    if (!q.items.length) {
      const empty = uiNode("li", "", "empty-state");
      const find = uiAction("Find a song", () =>
        window.resonanceBrowse.selectView("search"),
      );
      find.classList.add("primary");
      empty.append(
        uiNode("h2", "Your next song starts here."),
        uiNode(
          "p",
          "Choose music from your library. Play starts listening; Add to queue saves a song for later.",
        ),
        find,
      );
      userItems.append(empty);
    }
    for (const [localIndex, item] of q.items
      .slice(occurrenceLimit - 100, occurrenceLimit)
      .entries()) {
      const index = localIndex + occurrenceLimit - 100;
      const row = occurrenceRow(item, index, () => selectQueueItem(q, item.id));
      const state = window.resonanceListening.get();
      const section =
        index < state.selectedIndex
          ? "Earlier in your queue"
          : item.id === q.current_item_id
            ? state.followsQueue
              ? "Now Playing"
              : "Saved queue position"
            : "Up Next";
      const previousIndex = index - 1;
      const priorSection =
        previousIndex < state.selectedIndex
          ? "Earlier in your queue"
          : q.items[previousIndex]?.id === q.current_item_id
            ? state.followsQueue
              ? "Now Playing"
              : "Saved queue position"
            : "Up Next";
      if (localIndex === 0 || section !== priorSection)
        userItems.append(uiNode("li", section, "queue-section"));
      if (item.id === q.current_item_id) {
        row.classList.add("is-selected");
        row.append(
          uiNode("span", state.followsQueue ? "Now Playing" : "Saved", "badge"),
        );
      }
      if (item.last_skip_code)
        row.append(uiNode("span", "Skipped · unavailable", "badge"));
      const actions = [
        ["Play now", () => selectQueueItem(q, item.id)],
        [
          "Remove",
          async () => {
            await userWrite("DELETE", `/api/v1/queue/items/${item.id}`, {
              expected_version: q.revision,
            });
            await loadUserView(true);
          },
        ],
      ];
      if (index > 0)
        actions.push(["Move up", () => reorderQueue(q, index, -1)]);
      if (index < q.items.length - 1)
        actions.push(["Move down", () => reorderQueue(q, index, 1)]);
      const inline = uiNode("span", "", "queue-inline");
      for (const [label, fn] of actions.filter(([label]) =>
        ["Move up", "Move down", "Remove"].includes(label),
      )) {
        const button = uiAction(label, fn);
        window.resonanceUI.button(
          button,
          label === "Move up"
            ? "arrow-up"
            : label === "Move down"
              ? "arrow-down"
              : "x",
          `${label} ${trackContext(item)} · queue position ${index + 1}`,
          true,
        );
        inline.append(button);
      }
      row.append(inline);
      occurrenceMenu(row, item, actions);
      userItems.append(row);
    }
    if (q.items.length > occurrenceLimit) userMore.hidden = false;
    userStatus.textContent = q.items.length
      ? `${q.items.length} ${q.items.length === 1 ? "song" : "songs"} in your queue`
      : "";
  }
  async function renderPlaylists(reset) {
    if (currentPlaylist) return renderPlaylistDetail();
    if (reset) {
      userPageIndex = 0;
      userPageCursors = [null];
      viewCursor = null;
      userItems.replaceChildren();
    }
    const page = await viewAPI(
      `/api/v1/playlists?limit=50${viewCursor ? `&cursor=${encodeURIComponent(viewCursor)}` : ""}`,
    );
    for (const playlist of page.items) {
      const row = document.createElement("li");
      row.className = "playlist-card";
      const playlistButton = uiAction(playlist.name, () =>
        window.resonanceBrowse.openPlaylist(playlist.id),
      );
      playlistButton.className = "cover-card";
      playlistButton.setAttribute("aria-label", playlist.name);
      const monogram = window.resonanceUI.collectionMark(
        playlist.name,
        playlist.id,
      );
      playlistButton.replaceChildren(
        monogram,
        uiNode("strong", playlist.name),
        uiNode(
          "span",
          `${playlist.track_count || 0} ${playlist.track_count === 1 ? "song" : "songs"} · Updated ` +
            new Intl.DateTimeFormat(undefined, {
              month: "short",
              day: "numeric",
            }).format(new Date(playlist.updated_at)),
          "cover-credit",
        ),
      );
      row.append(playlistButton);
      userItems.append(row);
    }
    viewCursor = page.next_cursor;
    userMore.hidden = !viewCursor;
    userPrevious.hidden = userPageIndex === 0;
    userItems.className = "playlist-grid";
    userStatus.textContent = userItems.children.length
      ? `${page.items.length} ${page.items.length === 1 ? "playlist" : "playlists"}`
      : "No playlists yet";
  }
  async function reorderPlaylist(index, delta) {
    const before = window.resonanceRows.capture(userItems);
    const item = currentPlaylist.items[index];
    const order = currentPlaylist.items.map((item) => item.id);
    [order[index], order[index + delta]] = [order[index + delta], order[index]];
    await userWrite("PUT", `/api/v1/playlists/${currentPlaylist.id}/order`, {
      item_ids: order,
      expected_version: currentPlaylist.revision,
    });
    occurrenceLimit = (Math.floor((index + delta) / 100) + 1) * 100;
    await renderPlaylistDetail();
    window.resonanceRows.settle(userItems, before, item.id);
    userStatus.textContent = `${item.title || "Song"} moved to position ${index + delta + 1}.`;
  }
  async function renderPlaylistDetail() {
    const id =
      typeof currentPlaylist === "string"
        ? currentPlaylist
        : currentPlaylist.id;
    const detail = await viewAPI(`/api/v1/playlists/${id}`);
    currentPlaylist = detail;
    userTitle.textContent = detail.name;
    document.querySelector("#collection-links").hidden = true;
    document.querySelector("#view-description").textContent =
      "A sequence from your own collection.";
    document.querySelector("#playlist-back").hidden = false;
    userItems.replaceChildren();
    userMore.hidden = true;
    const controls = uiNode("li", "", "view-controls");
    userItems.className = "playlist-tracks";
    controls.append(
      uiAction("Rename", async () => {
        const { close, content, current } = openActionDialog("Rename playlist");
        const label = uiNode("label", "Playlist name");
        label.htmlFor = "rename-input";
        const input = document.createElement("input");
        input.id = "rename-input";
        input.value = detail.name;
        input.maxLength = 256;
        content.append(
          label,
          input,
          uiAction("Save name", async () => {
            await userWrite("PATCH", `/api/v1/playlists/${detail.id}`, {
              name: input.value,
              expected_version: detail.revision,
            });
            if (
              activeView === "playlists" &&
              currentPlaylist?.id === detail.id
            ) {
              const returnFocus = current();
              if (returnFocus) close();
              await renderPlaylistDetail();
              if (returnFocus)
                userItems
                  .querySelector(".view-controls button")
                  ?.focus({ preventScroll: true });
            }
          }),
        );
        input.focus();
      }),
      uiAction("Delete playlist", () => {
        const { content, close, current } =
          openActionDialog("Delete playlist?");
        content.append(
          uiNode(
            "p",
            `“${detail.name}” and its sequence will be deleted. The songs remain in your library.`,
          ),
        );
        const keep = uiAction("Keep playlist", close);
        const remove = uiAction("Delete permanently", async () => {
          await userWrite(
            "DELETE",
            `/api/v1/playlists/${detail.id}`,
            { expected_version: detail.revision },
            false,
          );
          const returnFocus = current();
          close();
          if (activeView === "playlists" && currentPlaylist?.id === detail.id) {
            window.resonanceBrowse.selectView("playlists");
            if (returnFocus) newPlaylist.focus({ preventScroll: true });
          }
        });
        remove.classList.add("danger");
        content.append(keep, remove);
        keep.focus();
      }),
    );
    for (const button of controls.querySelectorAll("button"))
      window.resonanceUI.button(
        button,
        button.textContent === "Rename" ? "pencil-simple" : "trash",
        button.textContent,
      );
    const hero = uiNode("li", "", "playlist-heading");
    const copy = uiNode("div", "", "summary-copy");
    copy.append(
      uiNode("p", "PLAYLIST", "eyebrow"),
      uiNode(
        "p",
        `${detail.items.length} ${detail.items.length === 1 ? "song" : "songs"}${detail.items.some((item) => !item.available) ? ` · ${detail.items.filter((item) => !item.available).length} unavailable` : ""}`,
        "item-subtitle",
      ),
    );
    const toolbar = uiNode("div", "", "playlist-toolbar");
    const playPlaylist = uiAction("Play playlist", () =>
      addCollection(detail.items, "now"),
    );
    playPlaylist.classList.add("primary");
    playPlaylist.disabled = !detail.items.some((item) => item.available);
    toolbar.append(
      playPlaylist,
      uiAction("Add to queue", () => addCollection(detail.items, "end")),
      uiAction("Add songs", () =>
        window.resonancePlaylistPicker(detail, async () => {
          if (activeView !== "playlists" || currentPlaylist?.id !== detail.id)
            return;
          await renderPlaylistDetail();
          userItems
            .querySelector(".playlist-toolbar button:last-child")
            ?.focus();
        }),
      ),
    );

    copy.append(toolbar);
    const mark = window.resonanceUI.collectionMark(detail.name, detail.id);
    mark.classList.add("playlist-detail-mark");
    hero.append(mark, copy);
    userItems.append(hero, controls);
    if (!detail.items.length) {
      const empty = uiNode("li", "", "empty-state");
      empty.append(
        uiNode("h2", "Start with one song."),
        uiNode(
          "p",
          "Choose Add songs above to find music in your collection. You can also add a song from its actions anywhere in the library.",
        ),
      );
      userItems.append(empty);
    }
    occurrenceLimit = Math.min(
      occurrenceLimit,
      Math.max(100, Math.ceil(detail.items.length / 100) * 100),
    );
    userPrevious.hidden = occurrenceLimit === 100;
    for (const [localIndex, item] of detail.items
      .slice(occurrenceLimit - 100, occurrenceLimit)
      .entries()) {
      const index = localIndex + occurrenceLimit - 100;
      const row = occurrenceRow(item, index);
      const actions = [
        [
          "Play now",
          async () =>
            addQueue(await userAPI(`/api/v1/tracks/${item.track_id}`), "now"),
        ],
        [
          "Remove",
          async () => {
            await userWrite(
              "DELETE",
              `/api/v1/playlists/${detail.id}/items/${item.id}`,
              { expected_version: detail.revision },
            );
            await renderPlaylistDetail();
          },
        ],
      ];
      if (index > 0)
        actions.push(["Move up", () => reorderPlaylist(index, -1)]);
      if (index < detail.items.length - 1)
        actions.push(["Move down", () => reorderPlaylist(index, 1)]);
      occurrenceMenu(row, item, actions);
      userItems.append(row);
    }
    if (detail.items.length > occurrenceLimit) userMore.hidden = false;
    userStatus.textContent = "";
  }
  async function renderFavorites(reset) {
    const generation = ++favoriteLoadGeneration;
    if (reset) {
      userPageIndex = 0;
      userPageCursors = [null];
      viewCursor = null;
      userItems.replaceChildren();
    }
    const page = await viewAPI(
      `/api/v1/favorites?limit=50${viewCursor ? `&cursor=${encodeURIComponent(viewCursor)}` : ""}`,
    );
    if (generation !== favoriteLoadGeneration || activeView !== "favorites")
      return;
    for (const item of page.items) {
      favorites.seed(item.track_id, true);
      const row = occurrenceRow({ ...item, id: item.track_id }, 0);
      row.querySelector(".track-number")?.remove();
      const remove = uiAction("Remove favorite", async () => {
        await favorites.save(item.track_id, false);
        favorites.seed(item.track_id, false);
        await renderFavorites(true);
      });
      window.resonanceUI.button(
        remove,
        "heart-fill",
        "Remove favorite " + trackContext(item),
        true,
      );
      remove.setAttribute("aria-pressed", "true");
      row.append(remove);
      userItems.append(row);
    }
    viewCursor = page.next_cursor;
    userMore.hidden = !viewCursor;
    userPrevious.hidden = userPageIndex === 0;
    userStatus.textContent = userItems.children.length
      ? `${userItems.children.length} ${userItems.children.length === 1 ? "favorite" : "favorites"}`
      : "No favorites yet";
    if (!page.items.length) {
      const empty = uiNode("li", "", "empty-state");
      empty.append(
        uiNode("h2", "Keep the songs you return to."),
        uiNode(
          "p",
          "Choose Favorite in a song’s actions. Your saved songs will be here whenever you want them.",
        ),
      );
      empty.append(
        uiAction("Find a song", () =>
          window.resonanceBrowse.selectView("search"),
        ),
      );
      userItems.append(empty);
    }
  }
  async function renderHistory(reset) {
    if (reset) {
      userPageIndex = 0;
      userPageCursors = [null];
      viewCursor = null;
      userItems.replaceChildren();
    }
    const page = await viewAPI(
      `/api/v1/history?limit=50${viewCursor ? `&cursor=${encodeURIComponent(viewCursor)}` : ""}`,
    );
    for (const item of page.items) {
      const row = document.createElement("li");
      row.className = "track-row";
      const copy = uiNode("div", "", "track-copy");
      const replay = uiAction(item.title || "Untitled track", async () =>
        window.resonanceBrowse.play(
          await userAPI(`/api/v1/tracks/${item.track_id}`),
        ),
      );
      replay.className = "item-title";
      replay.disabled = !item.available;
      copy.append(
        replay,
        uiNode(
          "span",
          [
            item.artist_credit || "Unknown artist",
            item.completed_at ? "Completed" : "Listened",
            new Intl.DateTimeFormat(undefined, {
              month: "short",
              day: "numeric",
              hour: "numeric",
              minute: "2-digit",
            }).format(new Date(item.started_at)),
          ].join(" · "),
          "item-subtitle",
        ),
      );
      row.append(window.resonanceBrowse.artwork(null), copy);
      window.resonanceBrowse.watchArtwork(
        row.querySelector("img"),
        item.track_id,
      );
      userItems.append(row);
    }
    viewCursor = page.next_cursor;
    userMore.hidden = !viewCursor;
    userPrevious.hidden = userPageIndex === 0;
    userStatus.textContent = userItems.children.length
      ? `${userItems.children.length} recent ${userItems.children.length === 1 ? "listen" : "listens"}`
      : "";
    if (!page.items.length) {
      const empty = uiNode("li", "", "empty-state");
      const find = uiAction("Find a song", () =>
        window.resonanceBrowse.selectView("search"),
      );
      find.classList.add("primary");
      empty.append(
        uiNode("h2", "Your listening, remembered."),
        uiNode(
          "p",
          "Listen for a little while, or finish a song, and it will appear here.",
        ),
        find,
      );
      userItems.append(empty);
    }
  }
  async function loadUserView(reset = false) {
    if (!activeView) return;
    const generation = userViewGeneration;
    userPrevious.disabled = true;
    userStatus.textContent = "Loading…";
    userMore.hidden = true;
    const mode = activeView;
    try {
      if (mode === "queue") await renderQueue();
      else if (mode === "playlists") await renderPlaylists(reset);
      else if (mode === "favorites") await renderFavorites(reset);
      else if (mode === "history") await renderHistory(reset);
    } catch (error) {
      if (
        mode === "playlists" &&
        currentPlaylist &&
        error.code === "not_found"
      ) {
        userTitle.textContent = "Playlist unavailable";
        userStatus.textContent =
          "This playlist is no longer here. Choose All playlists to return to your collection.";
        userItems.replaceChildren();
      } else userFailure(error);
    } finally {
      if (generation === userViewGeneration) userPrevious.disabled = false;
    }
  }

  async function playerQueueActions(snapshot, itemID, anchor) {
    const index = snapshot.items.findIndex((item) => item.id === itemID);
    if (index < 0) return;
    const item = snapshot.items[index];
    const { close, content } = window.resonanceMenus.open(
      anchor,
      item.title || "Queue item",
    );
    const add = (label, fn) =>
      content.append(
        uiAction(label, async () => {
          await fn();
          close();
          await refreshQueue();
        }),
      );
    if (item.available)
      add("Play now", () => selectQueueItem(snapshot, item.id));
    add("Remove", () =>
      userWrite("DELETE", `/api/v1/queue/items/${item.id}`, {
        expected_version: snapshot.revision,
      }),
    );
    for (const [label, delta] of [
      ["Move up", -1],
      ["Move down", 1],
    ])
      if (index + delta >= 0 && index + delta < snapshot.items.length)
        add(label, async () => {
          const order = snapshot.items.map((item) => item.id);
          [order[index], order[index + delta]] = [
            order[index + delta],
            order[index],
          ];
          await userWrite("PUT", "/api/v1/queue/order", {
            item_ids: order,
            expected_version: snapshot.revision,
          });
        });
  }
  async function resumeSelection(expected) {
    const snapshot = await refreshQueue();
    if (
      snapshot.selection_state !== "selected" ||
      snapshot.current_item_id !== expected.itemID ||
      snapshot.selection_token !== expected.token
    )
      throw { code: "stale_selection", status: 409 };
    const item = snapshot.items.find((item) => item.id === expected.itemID);
    if (!item?.available) throw { code: "track_unavailable", status: 503 };
    const track = await userAPI(`/api/v1/tracks/${item.track_id}`);
    window.resonanceBrowse.playTrack(track, expected);
  }
  window.resonanceUser = {
    playContext,
    async toggleShuffle() {
      const operation = queueWrites
        .catch(() => {})
        .then(async () => {
          const q = await refreshQueue();
          const modes = window.resonancePlaybackModes;
          const state = modes.get();
          const ids = state.shuffle
            ? modes.restoredOrder(q, state.original)
            : modes.upcomingOrder(q);
          if (ids.some((id, index) => id !== q.items[index].id)) {
            await userWrite("PUT", "/api/v1/queue/order", {
              item_ids: ids,
              expected_version: q.revision,
            });
            await refreshQueue();
          }
          modes.setShuffle(
            !state.shuffle,
            state.shuffle ? [] : q.items.map((item) => item.id),
          );
          userStatus.textContent = state.shuffle
            ? "Shuffle off. Upcoming songs restored where possible."
            : "Shuffle on. Up Next shows the playing order.";
          if (activeView === "queue") await loadUserView(true);
        });
      queueWrites = operation;
      return operation;
    },
    resumeSelection,
    readQueue: refreshQueue,
    selectQueueItem,
    addCollection,
    playerQueueActions,
    selectView: selectUserView,
    openPlaylist: (id) => selectUserView("playlists", id),
    onBrowseView,
    decorateTrackRow,
    setPlayback,
    addQueue,
    toggleFavorite,
    choosePlaylist,
    openActionDialog,
    isFavorite: (id) => favorites.get(id).present,
    favoriteStatus: (id) => favorites.get(id),
    loadFavorites: (ids) => favorites.load(ids),
    retryPending,
  };
  for (const id of ["next", "player-next"])
    document
      .querySelector(`#${id}`)
      .addEventListener("click", () =>
        advanceQueue(
          "next",
          listeningSession.current()?.decoderFailed ? "resolver_failed" : null,
        ).catch(userFailure),
      );
  for (const id of ["previous", "player-previous"])
    document
      .querySelector(`#${id}`)
      .addEventListener("click", () =>
        advanceQueue("previous").catch(userFailure),
      );
  document.querySelector("#clear-queue").addEventListener("click", async () => {
    try {
      const q = await refreshQueue();
      await userWrite("DELETE", "/api/v1/queue", {
        expected_version: q.revision,
      });
      userAudio.pause();
      const playback = listeningSession.current();
      if (playback) setPlayback(playback.track, null);
      await loadUserView(true);
    } catch (error) {
      userFailure(error);
    }
  });
  let creatingPlaylist = false;
  newPlaylist.addEventListener("click", () => {
    createPlaylistError.hidden = true;
    createPlaylistDialog.showModal();
    document.querySelector("#playlist-name").focus();
  });
  document
    .querySelector("#close-create-playlist")
    .addEventListener("click", () => createPlaylistDialog.close());
  playlistCreate.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (creatingPlaylist) return;
    creatingPlaylist = true;
    const button = playlistCreate.querySelector('button[type="submit"]');
    button.disabled = true;
    button.setAttribute("aria-busy", "true");
    createPlaylistError.hidden = true;
    try {
      await userWrite("POST", "/api/v1/playlists", {
        name: document.querySelector("#playlist-name").value,
        expected_version: 0,
      });
      document.querySelector("#playlist-name").value = "";
      await loadUserView(true);
      if (createPlaylistDialog.open) createPlaylistDialog.close();
    } catch (error) {
      userFailure(error);
      createPlaylistError.textContent = userStatus.textContent;
      createPlaylistError.hidden = false;
    } finally {
      creatingPlaylist = false;
      button.disabled = false;
      button.removeAttribute("aria-busy");
    }
  });
  userMore.addEventListener(
    "click",
    (event) => {
      if (!activeView) return;
      event.stopImmediatePropagation();
      if (activeView === "queue" || currentPlaylist) {
        if (activeView === "queue") queuePagePinned = true;
        occurrenceLimit += 100;
        loadUserView(true);
      } else {
        userPageCursors[++userPageIndex] = viewCursor;
        userItems.replaceChildren();
        loadUserView(false);
      }
    },
    true,
  );
  userPrevious.addEventListener(
    "click",
    (event) => {
      if (!activeView) return;
      event.stopImmediatePropagation();
      if (activeView === "queue" || currentPlaylist) {
        if (activeView === "queue") queuePagePinned = true;
        occurrenceLimit = Math.max(100, occurrenceLimit - 100);
        loadUserView(true);
      } else {
        userPageIndex = Math.max(0, userPageIndex - 1);
        viewCursor = userPageCursors[userPageIndex];
        userItems.replaceChildren();
        loadUserView(false);
      }
    },
    true,
  );
  retryPending()
    .then(() => Promise.allSettled([refreshFavorites()]))
    .then(() => {
      for (const row of userItems.querySelectorAll("[data-track-id]")) {
        const button = row.querySelector(".favorite-button");
        if (!button) continue;
        const saved = favorites.get(row.dataset.trackId).present;
        window.resonanceUI.button(
          button,
          saved ? "heart-fill" : "heart",
          button.getAttribute("aria-label"),
          true,
        );
        button.setAttribute("aria-pressed", String(saved));
      }
    });
})();
