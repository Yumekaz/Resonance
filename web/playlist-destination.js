// A bounded, searchable destination picker. No startup traversal of playlists.
window.resonancePlaylistDestination = async (
  track,
  operation = null,
  options = {},
) => {
  const { close, content, current, dialog } =
    window.resonanceUser.openActionDialog(options.title || "Add to playlist");
  const { node } = window.resonanceBrowse;
  content.append(
    node(
      "p",
      [
        track.title || "Untitled track",
        track.artist_credit || "Unknown artist",
      ].join(" · "),
    ),
  );
  const label = node("label", "Find a playlist");
  label.htmlFor = "playlist-destination-query";
  const input = document.createElement("input");
  input.id = "playlist-destination-query";
  input.type = "search";
  input.maxLength = 120;
  input.placeholder = "Playlist name";
  const select = document.createElement("select");
  select.setAttribute("aria-label", "Choose playlist");
  const feedback = node("p", "Loading playlists…", "hint");
  feedback.setAttribute("role", "status");
  const paging = node("div", "", "view-controls");
  const previous = node("button", "Previous playlists"),
    more = node("button", "More playlists");
  previous.type = more.type = "button";
  previous.hidden = more.hidden = true;
  paging.append(previous, more);
  const confirmLabel = options.confirmLabel || "Add track";
  const add = node("button", confirmLabel, "primary");
  add.type = "button";
  add.disabled = true;
  const create = node("button", "Create a playlist");
  create.type = "button";
  create.hidden = true;
  content.append(label, input, select, feedback, paging, add, create);
  let controller = new AbortController(),
    generation = 0,
    timer,
    next = null,
    pageIndex = 0,
    cursors = [null];
  dialog.addEventListener(
    "close",
    () => {
      controller.abort();
      clearTimeout(timer);
      generation++;
    },
    { once: true },
  );
  async function load(index = 0) {
    controller.abort();
    controller = new AbortController();
    const observed = ++generation;
    add.disabled = true;
    previous.disabled = more.disabled = true;
    select.replaceChildren(new Option("Choose playlist", ""));
    feedback.textContent = "Finding playlists…";
    const query = input.value.trim();
    try {
      const data = await window.resonanceAPI.read(
        `/api/v1/playlists?limit=50${query ? `&q=${encodeURIComponent(query)}` : ""}${cursors[index] ? `&cursor=${encodeURIComponent(cursors[index])}` : ""}`,
        { signal: controller.signal },
      );
      if (!current() || observed !== generation) return;
      const eligible = data.items.filter(
        (playlist) => playlist.id !== options.excludePlaylistID,
      );
      for (const playlist of eligible)
        select.add(new Option(playlist.name, playlist.id));
      pageIndex = index;
      next = data.next_cursor;
      previous.hidden = index === 0;
      more.hidden = !next;
      create.hidden = !!eligible.length;
      feedback.textContent = eligible.length
        ? `Choose a playlist, then ${confirmLabel}.`
        : query
          ? "No matching playlists. Try another name."
          : "No playlists yet. Create a sequence for this song.";
    } catch (error) {
      if (error.name !== "AbortError" && current())
        feedback.textContent =
          "Playlists are unavailable. Check the server, then search again.";
    } finally {
      if (current() && observed === generation)
        previous.disabled = more.disabled = false;
    }
  }
  input.addEventListener("input", () => {
    controller.abort();
    generation++;
    clearTimeout(timer);
    cursors = [null];
    pageIndex = 0;
    add.disabled = true;
    timer = setTimeout(() => load(0), 180);
  });
  select.addEventListener("change", () => {
    add.disabled = !select.value;
  });
  more.addEventListener("click", () => {
    cursors[pageIndex + 1] = next;
    load(pageIndex + 1);
  });
  previous.addEventListener("click", () => load(Math.max(0, pageIndex - 1)));
  create.addEventListener("click", () => {
    close();
    window.resonanceBrowse.selectView("playlists");
    document.querySelector("#new-playlist").click();
  });
  add.addEventListener("click", async () => {
    if (add.disabled || !select.value) return;
    const destination = select.value;
    add.disabled = true;
    add.setAttribute("aria-busy", "true");
    try {
      const playlist = await window.resonanceAPI.read(
        `/api/v1/playlists/${destination}`,
      );
      if (operation) await operation(playlist);
      else
        await window.resonanceAPI.write(
          "POST",
          `/api/v1/playlists/${playlist.id}/items`,
          { track_id: track.id, expected_version: playlist.revision },
        );
      if (current()) {
        close();
        document.querySelector("#status").textContent =
          `${options.successVerb || "Added"} to ${playlist.name}.`;
      }
    } catch (error) {
      if (current())
        feedback.textContent =
          error.code === "stale_version"
            ? `This playlist changed elsewhere. Try ${confirmLabel} again.`
            : error.code === "limit_exceeded"
              ? "This playlist is full. Choose another playlist. No songs were added or moved."
              : operation
                ? `The selection could not be saved. Check the server and try ${confirmLabel} again.`
                : "The song was not added. Check the server and try Add track again.";
    } finally {
      add.disabled = !select.value;
      add.removeAttribute("aria-busy");
    }
  });
  input.focus();
  await load();
};
