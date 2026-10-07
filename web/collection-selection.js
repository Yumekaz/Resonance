// Selected occurrence IDs belong to one collection. Queue/song playback state
// remains in its existing controller; this module owns only editing interaction.
(() => {
  const bar = document.getElementById("collection-edit");
  const toggle = document.getElementById("select-songs");
  const tools = document.getElementById("selection-tools");
  let state = null,
    scope = null,
    enabled = false,
    busy = false;
  const selected = new Set();
  const actionButtons = new Map();
  function button(id, label, fn) {
    const node = document.getElementById(id);
    node.addEventListener("click", fn);
    actionButtons.set(id, node);
    return node;
  }
  function picked() {
    return state ? state.items.filter((item) => selected.has(item.id)) : [];
  }
  function render() {
    const available = !!state?.items.length;
    bar.hidden = !available || !enabled;
    toggle.hidden = !available;
    tools.hidden = !enabled;
    toggle.setAttribute("aria-pressed", String(enabled));
    window.resonanceUI.button(
      toggle,
      enabled ? "x" : "check",
      enabled ? "Done selecting" : "Select songs",
    );
    document.getElementById("selection-count").textContent =
      `${selected.size} selected`;
    document.getElementById("select-matches").textContent = state
      ? `Select all ${state.items.length} ${state.items.length === 1 ? "song" : "songs"}`
      : "Select all";
    for (const label of document.querySelectorAll("#items .selection-check")) {
      label.hidden = !enabled;
      const input = label.querySelector("input");
      input.checked = selected.has(input.value);
      input.disabled = busy;
    }
    for (const [id, node] of actionButtons) {
      node.disabled =
        busy ||
        (!selected.size &&
          !["select-page", "select-matches", "clear-selection"].includes(id));
      if (
        state?.kind === "playlist" &&
        state.reorder === false &&
        ["selected-top", "selected-end"].includes(id)
      )
        node.disabled = true;
      node.hidden =
        (id === "selected-next" && state?.kind !== "queue") ||
        (id === "selected-top" && state?.kind !== "playlist") ||
        (["selected-queue-next", "selected-queue-end"].includes(id) &&
          state?.kind !== "playlist");
      if (
        ["selected-queue-next", "selected-queue-end"].includes(id) &&
        selected.size > 1000
      )
        node.disabled = true;
    }
    if (selected.size && !picked().some((item) => item.available))
      document.getElementById("selected-play").disabled = true;
    toggle.disabled = busy;
  }
  function update(snapshot) {
    const next = `${snapshot.kind}:${snapshot.id || ""}`;
    if (scope !== next) {
      selected.clear();
      enabled = false;
      scope = next;
    }
    state = snapshot;
    const valid = new Set(snapshot.items.map((item) => item.id));
    for (const id of selected) if (!valid.has(id)) selected.delete(id);
    render();
  }
  function decorate(row, item, index) {
    const label = document.createElement("label");
    label.className = "selection-check";
    label.hidden = !enabled;
    const input = document.createElement("input");
    input.type = "checkbox";
    input.value = item.id;
    input.setAttribute(
      "aria-label",
      `Select ${item.title || "Untitled track"} by ${item.artist_credit || "Unknown artist"} · position ${index + 1}`,
    );
    input.checked = selected.has(item.id);
    input.addEventListener("change", () => {
      if (input.checked) selected.add(item.id);
      else selected.delete(item.id);
      render();
    });
    label.append(input);
    row.prepend(label);
  }
  async function perform(
    action,
    target = null,
    placement = "",
    propagate = false,
    captured = null,
  ) {
    if (busy || (!captured && (!state || !selected.size))) return;
    const observed = captured?.state || state,
      items = captured?.items || picked();
    if (!items.length) return;
    busy = true;
    render();
    try {
      const body = {
        source: {
          kind: observed.kind,
          ...(observed.id ? { id: observed.id } : {}),
        },
        item_ids: items.map((item) => item.id),
        action,
        expected_version: observed.revision,
      };
      if (target) {
        body.target = {
          kind: target.kind,
          ...(target.id ? { id: target.id } : {}),
        };
        body.target_version = target.revision;
        body.placement = placement;
      }
      await window.resonanceAPI.write("POST", "/api/v1/collections/edit", body);
      if ((action === "remove" || action === "move") && state === observed)
        selected.clear();
      await window.resonanceUser.readQueue();
      if (scope === `${observed.kind}:${observed.id || ""}`)
        await observed.changed();
      document.getElementById("status").textContent =
        `${items.length} ${items.length === 1 ? "song" : "songs"} ${action === "remove" ? "removed" : action === "copy" ? "added" : "updated"}.`;
    } catch (error) {
      document.getElementById("status").textContent =
        error.code === "stale_version"
          ? "This collection changed. Review the selection and try again."
          : error.code === "limit_exceeded"
            ? "The destination is full. No songs were moved or added; choose another playlist or free some space."
            : "The selection could not be changed. No partial edit was saved.";
      if (scope === `${observed.kind}:${observed.id || ""}`)
        await observed.changed().catch(() => {});
      if (propagate) throw error;
    } finally {
      busy = false;
      render();
    }
  }
  toggle.addEventListener("click", () => {
    enabled = !enabled;
    if (!enabled) selected.clear();
    render();
  });
  button("select-page", "Select shown", () => {
    for (const row of document.querySelectorAll("#items [data-item-id]"))
      selected.add(row.dataset.itemId);
    render();
  });
  button("select-matches", "Select all", () => {
    for (const item of state.items) selected.add(item.id);
    render();
  });
  button("clear-selection", "Clear selection", () => {
    selected.clear();
    render();
  });
  button("selected-next", "Play next", () => perform("next"));
  button("selected-top", "Move to top", () => perform("top"));
  button("selected-end", "Move to end", () => perform("end"));
  async function copyToQueue(placement) {
    if (busy || !state || !selected.size) return;
    const captured = { state, items: picked() };
    busy = true;
    render();
    try {
      const q = await window.resonanceAPI.read("/api/v1/queue");
      busy = false;
      await perform(
        "copy",
        { kind: "queue", revision: q.revision },
        placement,
        false,
        captured,
      );
    } catch (_) {
      document.getElementById("status").textContent =
        "Up Next is unavailable. Your selection is unchanged; try again.";
    } finally {
      busy = false;
      render();
    }
  }
  button("selected-queue-next", "Play next", () => copyToQueue("next"));
  button("selected-queue-end", "Add to queue", () => copyToQueue("end"));
  button("selected-play", "Play selected", () => {
    const items = picked().filter((item) => item.available);
    if (!items.length) return;
    window.resonanceUser
      .playSource({
        kind: "selection",
        order: "original",
        track_ids: items.map((item) => item.track_id),
      })
      .catch(() => {
        document.getElementById("status").textContent =
          "The selection could not play. Try again.";
      });
  });
  function saveSelection(move = false) {
    if (busy || !state || !selected.size) return;
    const captured = { state, items: picked() };
    const count = selected.size;
    window.resonancePlaylistDestination(
      {
        title: `${count} selected songs`,
        artist_credit: state.kind === "queue" ? "Up Next" : "Playlist",
      },
      async (playlist) =>
        perform(
          move ? "move" : "copy",
          {
            kind: "playlist",
            id: playlist.id,
            revision: playlist.revision,
          },
          "",
          true,
          captured,
        ),
      {
        excludePlaylistID: state.kind === "playlist" ? state.id : null,
        title: move ? "Move selected songs to playlist" : "Add to playlist",
        confirmLabel: move ? "Move songs" : "Add songs",
        successVerb: move ? "Moved" : "Added",
      },
    );
  }
  button("selected-save", "Add to playlist", () => saveSelection());
  button("selected-move-save", "Move to playlist", () => saveSelection(true));
  button("selected-remove", "Remove selected", () => {
    const captured = { state, items: picked() };
    const { content, close } = window.resonanceUser.openActionDialog(
      "Remove selected songs?",
    );
    const copy = document.createElement("p");
    copy.textContent = `Remove ${selected.size} songs from ${state.kind === "queue" ? "Up Next" : "this playlist"}? Your music files stay in your library.`;
    const keep = document.createElement("button");
    keep.textContent = "Keep songs";
    keep.addEventListener("click", close);
    const remove = document.createElement("button");
    remove.textContent = "Remove selected";
    remove.className = "danger";
    remove.addEventListener("click", () => {
      close();
      perform("remove", null, "", false, captured);
    });
    content.append(copy, keep, remove);
    keep.focus();
  });
  window.resonanceSelection = {
    update,
    decorate,
    clear() {
      selected.clear();
      render();
    },
    hide() {
      state = null;
      selected.clear();
      enabled = false;
      scope = null;
      render();
    },
  };
})();
