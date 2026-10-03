// Add songs without leaving the playlist. Reads are bounded and cancellable.
window.resonancePlaylistPicker = async (playlist, onSaved) => {
  const { content, current, dialog } = window.resonanceUser.openActionDialog(
    `Add songs to ${playlist.name}`,
  );
  const { node, artwork } = window.resonanceBrowse;
  const label = node("label", "Find a song in your library");
  label.htmlFor = "playlist-song-query";
  const input = document.createElement("input");
  input.id = "playlist-song-query";
  input.type = "search";
  input.placeholder = "Track, artist, or album";
  input.maxLength = 120;
  const feedback = node(
    "p",
    "Search your collection, or choose a song below.",
    "hint",
  );
  feedback.setAttribute("role", "status");
  const list = node("ul", "", "picker-results");
  list.setAttribute("aria-label", "Songs to add");
  content.append(label, input, feedback, list);
  let controller = new AbortController(),
    generation = 0,
    timer,
    changed = false,
    pending = 0,
    closed = false;
  function settled() {
    if (closed && pending === 0 && changed) {
      changed = false;
      onSaved();
    }
  }
  dialog.addEventListener(
    "close",
    () => {
      controller.abort();
      clearTimeout(timer);
      generation++;
      closed = true;
      settled();
    },
    { once: true },
  );
  async function searchSongs() {
    controller.abort();
    controller = new AbortController();
    const observed = ++generation;
    const query = input.value.trim();
    feedback.textContent = "Finding songs…";
    list.replaceChildren();
    try {
      const result = await window.resonanceAPI.read(
        query
          ? `/api/v1/search?q=${encodeURIComponent(query)}&limit=20`
          : "/api/v1/tracks?limit=20",
        { signal: controller.signal },
      );
      if (!current() || observed !== generation) return;
      const tracks = query ? result.tracks : result.items;
      feedback.textContent = tracks.length
        ? "Choose Add. You can add a song again to repeat it."
        : "No matching songs. Try another title or artist.";
      for (const track of tracks) {
        const row = node("li", "", "track-row");
        const copy = node("div", "", "track-copy");
        copy.append(
          node("strong", track.title || "Untitled track"),
          node(
            "span",
            [track.artist_credit || "Unknown artist", track.album_title]
              .filter(Boolean)
              .join(" · "),
            "item-subtitle",
          ),
        );
        const add = node("button", "Add");
        add.setAttribute(
          "aria-label",
          `Add ${track.title || "Untitled track"} by ${track.artist_credit || "Unknown artist"} to ${playlist.name}`,
        );
        add.disabled = !track.available;
        add.addEventListener("click", async () => {
          if (add.disabled) return;
          add.disabled = true;
          add.setAttribute("aria-busy", "true");
          pending++;
          try {
            const fresh = await window.resonanceAPI.read(
              `/api/v1/playlists/${playlist.id}`,
            );
            await window.resonanceAPI.write(
              "POST",
              `/api/v1/playlists/${playlist.id}/items`,
              { track_id: track.id, expected_version: fresh.revision },
            );
            changed = true;
            if (current()) {
              feedback.textContent = `${track.title || "Song"} added to ${playlist.name}.`;
              add.textContent = "Add again";
            }
          } catch (error) {
            if (current())
              feedback.textContent =
                error.code === "stale_version"
                  ? "This playlist changed elsewhere. Choose Add again to retry."
                  : "The song could not be added. Check your connection, then retry Add.";
          } finally {
            add.disabled = false;
            add.removeAttribute("aria-busy");
            pending--;
            settled();
          }
        });
        row.append(
          artwork(
            track.artwork_url,
            "row-art",
            window.resonanceUI.artworkKey(track),
          ),
          copy,
          add,
        );
        list.append(row);
      }
    } catch (error) {
      if (error.name !== "AbortError" && current())
        feedback.textContent =
          "Your library is unavailable. Try searching again after reconnecting.";
    }
  }
  input.addEventListener("input", () => {
    controller.abort();
    generation++;
    clearTimeout(timer);
    timer = setTimeout(searchSongs, 180);
  });
  input.focus();
  await searchSongs();
};
