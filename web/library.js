(() => {
  // Navigation owns only the content region. The audio element lives for the
  // lifetime of the document, independently of views and pending reads.
  const items = document.querySelector("#items");
  const status = document.querySelector("#status");
  const title = document.querySelector("#view-title");
  const adaptHeading = () => {
    title.dataset.long = String(title.textContent.length > 70);
  };
  new MutationObserver(adaptHeading).observe(title, {
    childList: true,
    characterData: true,
    subtree: true,
  });
  adaptHeading();
  const more = document.querySelector("#more");
  const audio = document.querySelector("#audio");
  const nowTitle = document.querySelector("#now-title");
  const nowCredit = document.querySelector("#now-credit");
  const cover = document.querySelector("#cover");
  const playerError = document.querySelector("#player-error");
  const homeContent = document.querySelector("#home-content");
  const detailHero = document.querySelector("#detail-hero");
  const description = document.querySelector("#view-description");
  let view = "home",
    group = null,
    cursor = null,
    loading = false;
  let pageCursors = [null],
    pageIndex = 0;
  let navigationGeneration = 0,
    browseAbort = new AbortController(),
    searchTimer;
  let catalogOrder = "title",
    availableOnly = false;
  const catalogPositions = new Map(); // Three read-navigation descriptors, never cached catalog data.
  let loadedCatalogScope = null;
  let visibleTracks = [];
  const catalogScope = () =>
    `${view}:${catalogOrder}:${view === "tracks" && availableOnly}`;
  const placeholder = window.resonanceUI.fallback;
  // randomUUID is secure-context-only, but getRandomValues is available on LAN
  // HTTP. Use the same RFC 4122 v4 wire shape without weakening randomness.
  window.resonanceID = () => {
    if (crypto.randomUUID) return crypto.randomUUID();
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    bytes[6] = (bytes[6] & 15) | 64;
    bytes[8] = (bytes[8] & 63) | 128;
    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join(
      "",
    );
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  };

  function node(tag, text = "", className) {
    const el = document.createElement(tag);
    el.textContent = text;
    if (className) el.className = className;
    return el;
  }
  function action(text, fn, className) {
    const button = node("button", text, className);
    button.type = "button";
    if (!className?.includes("item-title")) {
      const icon = {
        "Play album": "play",
        "Play artist": "play",
        "Add to queue": "plus",
        "Back to albums": "arrow-left",
        "Back to artists": "arrow-left",
        "View tracks": "music-notes",
      }[text];
      if (icon) window.resonanceUI.button(button, icon, text);
    }
    button.addEventListener("click", async () => {
      if (button.disabled || button.getAttribute("aria-busy") === "true")
        return;
      button.setAttribute("aria-busy", "true");
      try {
        await fn();
      } catch (error) {
        if (error?.name !== "AbortError")
          status.textContent = "That action could not finish. Try again.";
      } finally {
        button.removeAttribute("aria-busy");
      }
    });
    return button;
  }
  function artwork(url, className = "row-art", key = "") {
    const img = document.createElement("img");
    img.alt = "";
    img.className = className;
    img.loading = "lazy";
    img.decoding = "async";
    window.resonanceUI.setArtwork(img, url, key);
    img.addEventListener("error", () => {
      if (img.dataset.missing !== "true")
        window.resonanceUI.setArtwork(img, null);
    });
    return img;
  }
  async function readAPI(path, signal = browseAbort.signal) {
    const response = await fetch(path, { cache: "no-store", signal });
    if (!response.ok)
      throw new Error(
        "The library is temporarily unavailable. Try again shortly.",
      );
    return response.json();
  }
  function endpoint() {
    return group
      ? `/api/v1/${group.kind}/${encodeURIComponent(group.id)}/${view}`
      : `/api/v1/${view}`;
  }
  function label(item, kind = view) {
    return kind === "artists"
      ? item.display_credit
      : kind === "albums"
        ? item.display_title
        : item.title || "Untitled track";
  }
  function trackRow(track, index) {
    const row = node("li", "", "track-row");
    row.dataset.trackId = track.id;
    if (group?.kind === "albums")
      row.append(
        node(
          "span",
          track.track_number
            ? `${track.disc_number || 1}.${track.track_number}`
            : String(index + 1),
          "track-number",
        ),
      );
    row.append(
      artwork(
        track.artwork_url,
        "row-art",
        window.resonanceUI.artworkKey(track),
      ),
    );
    const copy = node("div", "", "track-copy");
    const button = action(
      track.title || "Untitled track",
      () =>
        view === "tracks" && visibleTracks.some((item) => item.id === track.id)
          ? window.resonanceUser.playContext([...visibleTracks], track.id)
          : startTrack(track),
      "item-title",
    );
    button.disabled = !track.available;
    button.title = track.title || "Untitled track";
    const contextID = `track-credit-${track.id}`;
    const subtitle = node(
      "span",
      [track.artist_credit || "Unknown artist", track.album_title]
        .filter(Boolean)
        .join(" · "),
      "item-subtitle",
    );
    subtitle.id = contextID;
    button.setAttribute("aria-describedby", contextID);
    copy.append(button, subtitle);
    row.append(copy);
    const playbackMark = node("span", "", "badge playback-mark");
    playbackMark.hidden = true;
    row.append(playbackMark);
    if (!track.available) row.append(node("span", "Unavailable", "badge"));
    window.resonanceUser?.decorateTrackRow(row, track);
    return row;
  }
  function artFrame(img, playable = false, identity = "") {
    const frame = node("span", "", "artwork-frame");
    frame.append(img);
    if (identity) {
      const letter = node(
        "span",
        Array.from(identity.trim())[0]?.toUpperCase() || "♪",
        "fallback-letter",
      );
      letter.setAttribute("aria-hidden", "true");
      frame.append(letter);
    }
    if (playable) {
      const hint = node("span", "", "cover-play");
      hint.append(window.resonanceUI.icon("play"));
      frame.append(hint);
    }
    return frame;
  }
  function groupCredit(item, kind) {
    if (kind === "artists")
      return item.track_count === undefined
        ? "Artist"
        : `${item.track_count} ${item.track_count === 1 ? "song" : "songs"}`;
    return [
      item.artist_credit ||
        (item.track_count === undefined ? "Album" : "Unknown artist"),
      item.release_year,
    ]
      .filter(Boolean)
      .join(" · ");
  }
  function groupCard(item, kind) {
    const row = node("li");
    const button = action("", () => openGroup(kind, item), "cover-card");
    button.setAttribute("aria-label", label(item, kind));
    const img = artwork(
      item.artwork_url,
      "album-art",
      `${kind === "albums" ? "album" : "artist"}:${item.id}`,
    );
    const credit = node("span", groupCredit(item, kind), "cover-credit");
    credit.id = `group-credit-${kind}-${item.id}`;
    button.setAttribute("aria-describedby", credit.id);
    button.append(
      artFrame(img, false, label(item, kind)),
      node("strong", label(item, kind)),
      credit,
      node("span", "Library sleeve", "artwork-caption"),
    );
    row.append(button);
    img.dataset.kind = kind;
    img.dataset.id = item.id;
    if (item.track_count === undefined) {
      img.dataset.pending = "true";
      coverObserver.observe(img);
    }
    return row;
  }
  function homeTrackCard(item) {
    const row = node("li");
    const button = action(
      "",
      async () => startTrack(await readAPI(`/api/v1/tracks/${item.track_id}`)),
      "cover-card",
    );
    button.setAttribute("aria-label", item.title || "Untitled track");
    button.disabled = !item.available;
    const img = artwork(null, "album-art", `track:${item.track_id}`);
    button.append(
      artFrame(img, true, item.title || "Untitled track"),
      node("strong", item.title || "Untitled track"),
      node("span", item.artist_credit || "Unknown artist", "cover-credit"),
      node("span", "Library sleeve", "artwork-caption"),
    );
    const replay = node("span", "", "replay-hint");
    replay.append(window.resonanceUI.icon("play"), node("span", "Play again"));
    button.append(replay);
    row.append(button);
    img.dataset.kind = "tracks";
    img.dataset.pending = "true";
    img.dataset.id = item.track_id;
    coverObserver.observe(img);
    return row;
  }
  const coverJobs = [];
  let coverActive = 0;
  const artworkReads = new Map();
  function readArtworkMetadata(id, signal) {
    const cached = artworkReads.get(id);
    if (
      cached?.signal === signal &&
      (cached.expires === null || cached.expires > performance.now())
    )
      return cached.promise;
    const entry = { signal, expires: null, promise: null };
    entry.promise = readAPI(`/api/v1/tracks/${id}`, signal)
      .then((data) => {
        entry.expires = performance.now() + 30000;
        return { id, artwork_url: data.artwork_url, album_id: data.album_id };
      })
      .catch((error) => {
        if (artworkReads.get(id) === entry) artworkReads.delete(id);
        throw error;
      });
    artworkReads.delete(id);
    artworkReads.set(id, entry);
    while (artworkReads.size > 200)
      artworkReads.delete(artworkReads.keys().next().value);
    return entry.promise;
  }
  const coverObserver = new IntersectionObserver(
    (entries) => {
      for (const entry of entries)
        if (entry.isIntersecting) {
          coverObserver.unobserve(entry.target);
          coverJobs.push({ img: entry.target, signal: browseAbort.signal });
        }
      drainCoverJobs();
    },
    { rootMargin: "100px" },
  );
  function drainCoverJobs() {
    while (coverActive < 4 && coverJobs.length) {
      const { img, signal } = coverJobs.shift();
      if (signal.aborted || !img.isConnected) continue;
      coverActive++;
      (img.dataset.kind === "tracks"
        ? readArtworkMetadata(img.dataset.id, signal)
        : readAPI(
            `/api/v1/${img.dataset.kind}/${img.dataset.id}/tracks?limit=12`,
            signal,
          )
      )
        .then((page) => {
          if (!signal.aborted && img.isConnected) {
            const tracks = img.dataset.kind === "tracks" ? [page] : page.items;
            const t = tracks.find((t) => t.artwork_url);
            window.resonanceUI.setArtwork(
              img,
              t?.artwork_url,
              img.dataset.kind === "tracks" && tracks[0]
                ? window.resonanceUI.artworkKey(tracks[0])
                : img.dataset.sleeveKey,
            );
            const credit = img
              .closest(".cover-card, .result-row")
              ?.querySelector(".cover-credit");
            if (credit && img.dataset.kind === "albums")
              credit.textContent =
                tracks[0]?.album_artist_credit ||
                tracks[0]?.artist_credit ||
                "Unknown artist";
          }
        })
        .catch(() => {
          if (img.isConnected) img.dataset.pending = "false";
        })
        .finally(() => {
          coverActive--;
          drainCoverJobs();
        });
    }
  }
  function addItem(item, index = 0) {
    items.append(
      view === "tracks" ? trackRow(item, index) : groupCard(item, view),
    );
  }
  function emptyState(heading, message, target = homeContent) {
    const el = node("section", "", "empty-state");
    el.append(node("h2", heading), node("p", message));
    target.append(el);
  }
  function skeleton() {
    homeContent.replaceChildren();
    for (let i = 0; i < 3; i++) {
      const el = node("div", "", "skeleton");
      el.setAttribute("aria-hidden", "true");
      homeContent.append(el);
    }
  }
  async function load(reset = false, requestedIndex = null) {
    if (!["tracks", "artists", "albums"].includes(view) || loading) return;
    const generation = navigationGeneration;
    loading = true;
    if (reset) {
      cursor = null;
      pageCursors = [null];
      pageIndex = 0;
      items.replaceChildren();
      skeleton();
    }
    const targetIndex = reset
      ? 0
      : requestedIndex === null
        ? pageIndex + 1
        : requestedIndex;
    const requestCursor =
      targetIndex === 0
        ? null
        : requestedIndex === null
          ? cursor
          : pageCursors[targetIndex];
    items.className = view === "tracks" ? "" : "collection-grid";
    more.hidden = true;
    status.textContent = "Loading your music…";
    try {
      const url = new URL(endpoint(), location.origin);
      url.searchParams.set("limit", "50");
      if (!group) {
        if (catalogOrder === "title_desc")
          url.searchParams.set("order", catalogOrder);
        if (view === "tracks" && availableOnly)
          url.searchParams.set("available", "true");
      }
      if (requestCursor) url.searchParams.set("cursor", requestCursor);
      const page = await readAPI(url);
      if (generation !== navigationGeneration) return;
      homeContent.replaceChildren();
      items.replaceChildren();
      visibleTracks =
        view === "tracks" ? page.items.filter((item) => item.available) : [];
      pageCursors[targetIndex] = requestCursor;
      pageIndex = targetIndex;
      if (!group) loadedCatalogScope = catalogScope();
      page.items.forEach((item, i) => addItem(item, pageIndex * 50 + i));
      cursor = page.next_cursor;
      more.hidden = !cursor;
      document.querySelector("#previous-page").hidden = pageIndex === 0;
      status.textContent = items.children.length
        ? `${items.children.length} ${items.children.length === 1 ? view.slice(0, -1) : view}${pageIndex ? ` · page ${pageIndex + 1}` : ""}`
        : "Nothing here yet";
      if (!items.children.length)
        emptyState(
          "Your collection starts here.",
          "Music will appear here when it is available. Try another library view or check again later.",
        );
    } catch (error) {
      if (generation === navigationGeneration && error.name !== "AbortError") {
        homeContent.replaceChildren();
        status.textContent = error.message;
      }
    } finally {
      if (generation === navigationGeneration) loading = false;
    }
  }
  function beginView(nextView) {
    if (
      !group &&
      !loading &&
      ["tracks", "artists", "albums"].includes(view) &&
      loadedCatalogScope === catalogScope()
    )
      catalogPositions.set(view, {
        scope: loadedCatalogScope,
        pageIndex,
        pageCursors: [...pageCursors],
        scrollY: window.scrollY,
        focusID:
          document.activeElement.closest("#items li")?.dataset.trackId ||
          document.activeElement.closest("#items li")?.querySelector("img")
            ?.dataset.id,
      });
    window.resonanceMenus?.close(false);
    browseAbort.abort();
    browseAbort = new AbortController();
    navigationGeneration++;
    loading = false;
    clearTimeout(searchTimer);
    coverObserver.disconnect();
    coverJobs.length = 0;
    artworkReads.clear();
    window.resonanceUser?.onBrowseView(nextView);
    view = nextView;
    document.body.dataset.view = nextView;
    document.querySelector("#view-eyebrow").textContent =
      nextView === "home"
        ? new Intl.DateTimeFormat(undefined, {
            weekday: "long",
            month: "long",
            day: "numeric",
            year: "numeric",
          })
            .format(new Date())
            .toUpperCase()
        : "THE PERSONAL COLLECTION";
    group = null;
    document.querySelector("#browse-tools").hidden = ![
      "tracks",
      "artists",
      "albums",
    ].includes(nextView);
    if (["tracks", "artists", "albums"].includes(nextView))
      document.querySelector("#browse-tools").append(status);
    else homeContent.before(status);
    document.querySelector("#available-filter").hidden = nextView !== "tracks";
    cursor = null;
    items.replaceChildren();
    items.className = "";
    document.querySelector("#previous-page").hidden = true;
    homeContent.replaceChildren();
    detailHero.replaceChildren();
    detailHero.hidden = true;
    more.hidden = true;
    document.querySelector("#search-form").hidden = nextView !== "search";
    document.querySelector("#playlist-back").hidden = true;
    document.querySelector("#library-tabs").hidden = ![
      "tracks",
      "artists",
      "albums",
    ].includes(nextView);
    document.querySelector("#collection-links").hidden = ![
      "tracks",
      "artists",
      "albums",
      "favorites",
      "playlists",
      "history",
    ].includes(nextView);
    document.querySelector("#listening-summary").hidden = nextView !== "queue";
    syncNavigation(nextView);
    description.textContent =
      {
        home: "Your collection, ready when you are.",
        search: "Find the song you came for.",
        tracks: "Every song has a place.",
        artists: "The voices and sounds in your collection.",
        albums: "Records to spend a little time with.",
        queue: "The music playing here, and the songs you have lined up.",
        playlists: "A sequence for every kind of day.",
        favorites: "The songs you come back to.",
        history: "Recent listening, one song at a time.",
      }[nextView] || "";
    title.textContent =
      nextView === "home"
        ? homeHeading()
        : nextView === "queue"
          ? "Up Next"
          : nextView[0].toUpperCase() + nextView.slice(1);
    status.textContent = "";
  }
  function homeHeading() {
    return matchMedia("(max-width:700px)").matches
      ? "Music for today."
      : "Back to the music.";
  }
  function syncNavigation(mode) {
    for (const button of document.querySelectorAll("nav button[data-view]")) {
      const librarySelected =
        button.dataset.view === "library" &&
        (["tracks", "artists", "albums"].includes(mode) ||
          (matchMedia(
            "(max-width:700px), (max-height:550px) and (max-width:950px)",
          ).matches &&
            ["favorites", "playlists", "history"].includes(mode)));
      button.setAttribute(
        "aria-current",
        button.dataset.view === mode || librarySelected ? "page" : "false",
      );
    }
  }
  window.addEventListener("resize", () => {
    if (view === "home") title.textContent = homeHeading();
    syncNavigation(view);
  });
  document
    .querySelector("#playlist-back")
    .addEventListener("click", () => selectView("playlists"));
  document
    .querySelector("#mobile-search")
    .addEventListener("click", () => selectView("search"));
  document.querySelector(".mobile-brand").addEventListener("click", (event) => {
    event.preventDefault();
    selectView("home");
  });
  function selectView(nextView, updateURL = true) {
    if (nextView === "library") nextView = "albums";
    if (
      ![
        "home",
        "search",
        "tracks",
        "artists",
        "albums",
        "queue",
        "playlists",
        "favorites",
        "history",
      ].includes(nextView)
    )
      nextView = "home";
    beginView(nextView);
    if (updateURL) {
      history.pushState(null, "", `#${nextView}`);
      window.scrollTo(0, 0);
    }
    if (["queue", "playlists", "favorites", "history"].includes(nextView)) {
      window.resonanceUser?.selectView(nextView);
      return;
    }
    if (nextView !== "search") title.focus({ preventScroll: true });
    if (nextView === "home") {
      loadHome();
      return;
    }
    if (nextView === "search") {
      search();
      document.querySelector("#search-query").focus({ preventScroll: true });
      return;
    }
    const saved = catalogPositions.get(nextView);
    if (saved?.scope === catalogScope()) {
      pageCursors = [...saved.pageCursors];
      const generation = navigationGeneration;
      load(false, saved.pageIndex).then(() => {
        if (generation === navigationGeneration) {
          window.scrollTo(0, saved.scrollY);
          if (
            saved.focusID &&
            (document.activeElement === title ||
              document.activeElement === document.body)
          ) {
            const row = [...items.children].find(
              (row) =>
                row.dataset.trackId === saved.focusID ||
                row.querySelector("img")?.dataset.id === saved.focusID,
            );
            row
              ?.querySelector(".cover-card,.item-title")
              ?.focus({ preventScroll: true });
          }
        }
      });
    } else load(true);
  }
  async function loadHome() {
    const generation = navigationGeneration;
    skeleton();
    status.textContent = "Loading your collection…";
    try {
      const [albums, recent, favorites] = await Promise.all([
        readAPI("/api/v1/albums?limit=6"),
        readAPI("/api/v1/history?limit=12"),
        readAPI("/api/v1/favorites?limit=4"),
      ]);
      if (generation !== navigationGeneration) return;
      homeContent.replaceChildren();
      status.textContent = "";
      const playableRecent = recent.items.filter((item) => item.available);
      const playableFavorites = favorites.items.filter(
        (item) => item.available,
      );
      if (playableRecent.length) {
        const section = homeSection("Recently played", "history");
        section.classList.add("recent-section");
        const list = node("ul", "", "collection-grid");
        const seen = new Set();
        for (const item of playableRecent) {
          if (seen.has(item.track_id)) continue;
          seen.add(item.track_id);
          list.append(homeTrackCard(item));
          if (seen.size === 6) break;
        }
        section.classList.toggle("compact-shelf", seen.size < 3);
        section.classList.toggle("paired-shelf", seen.size === 2);
        section.append(list);
      }
      const section = homeSection("Your albums", "albums");
      const grid = node("ul", "", "collection-grid");
      for (const item of albums.items) grid.append(groupCard(item, "albums"));
      section.append(grid);
      if (!albums.items.length)
        emptyState(
          "A place for your music.",
          "Browse Tracks for music without album tags. Your albums will appear as your library grows.",
          section,
        );
      if (playableFavorites.length) {
        const section = homeSection("On repeat, by choice.", "favorites");
        const list = node("ul", "", "collection-grid");
        for (const item of playableFavorites) list.append(homeTrackCard(item));
        section.append(list);
      } else {
        const section = homeSection("Keep your favorites close.", "favorites");
        const empty = node("div", "", "empty-shelf");
        empty.append(
          artwork(null, "empty-art"),
          node(
            "p",
            "Save the songs that stay with you. Your favorites will be right here.",
            "hint",
          ),
        );
        empty.append(
          action("Find a favorite", () => selectView("tracks"), "text-button"),
        );
        section.append(empty);
      }
    } catch (error) {
      if (generation === navigationGeneration && error.name !== "AbortError") {
        homeContent.replaceChildren();
        status.textContent = error.message;
      }
    }
  }
  function homeSection(heading, viewName) {
    const section = node("section", "", "home-section");
    const top = node("div", "", "section-heading");
    top.append(
      node("h2", heading),
      action("View all", () => selectView(viewName)),
    );
    section.append(top);
    homeContent.append(section);
    return section;
  }
  function compactTrack(item) {
    const row = node("li", "", "track-row");
    row.append(artwork(null));
    const copy = node("div", "", "track-copy");
    copy.append(
      action(
        item.title || "Untitled track",
        async () =>
          startTrack(await readAPI(`/api/v1/tracks/${item.track_id}`)),
        "item-title",
      ),
      node("span", item.artist_credit || "Unknown artist", "item-subtitle"),
    );
    row.append(copy);
    return row;
  }
  async function openGroup(kind, item, updateURL = true) {
    const heading = label(item, kind);
    const parentHash = location.hash;
    const parentLabel = group
      ? title.textContent
      : view === "home"
        ? "Home"
        : view;
    beginView(kind === "artists" ? "albums" : "tracks");
    group = { kind, id: item.id };
    document.querySelector("#browse-tools").hidden = true;
    homeContent.before(status);
    const route = `#${kind}/${encodeURIComponent(item.id)}`;
    if (updateURL) {
      history.pushState({ route, parentHash, parentLabel }, "", route);
      window.scrollTo(0, 0);
    }
    title.textContent = heading;
    title.focus({ preventScroll: true });
    document.querySelector("#library-tabs").hidden = true;
    document.querySelector("#collection-links").hidden = true;
    description.textContent =
      kind === "artists"
        ? "Albums and tracks from your local collection."
        : "An album from your collection.";
    detailHero.hidden = false;
    detailHero.className = "detail-hero";
    const img = artwork(
      item.artwork_url,
      "detail-art",
      `${kind === "albums" ? "album" : "artist"}:${item.id}`,
    );
    img.dataset.pending = String(item.track_count === undefined);
    const body = node("div");
    const parent =
      history.state?.route === route && history.state.parentHash
        ? history.state
        : null;
    body.append(
      action(
        "Back to " + (parent?.parentLabel || kind),
        () => {
          if (parent) history.back();
          else selectView(kind);
        },
        "text-button",
      ),
    );
    const info = node(
      "p",
      kind === "artists"
        ? "Albums in your library credited to this artist."
        : "",
    );
    body.append(info);
    const controls = node("div", "", "view-controls");
    const trackPath = `/api/v1/${kind}/${item.id}/tracks`;
    controls.append(
      action(
        "Play " + (kind === "artists" ? "artist" : "album"),
        () => queueCollection(trackPath, true),
        "primary",
      ),
      action("Add to queue", () => queueCollection(trackPath, false)),
    );
    controls.querySelector(".primary").disabled = item.available === false;
    if (kind === "artists")
      controls.append(
        action("View tracks", () => {
          view = "tracks";
          load(true);
        }),
      );
    body.append(controls);
    detailHero.append(img, body);
    const generation = navigationGeneration;
    if (item.track_count !== undefined) {
      if (kind === "albums")
        info.textContent =
          groupCredit(item, kind) +
          ` · ${item.track_count} ${item.track_count === 1 ? "song" : "songs"}`;
    } else
      readAPI(trackPath + "?limit=12")
        .then((page) => {
          if (generation !== navigationGeneration) return;
          const art = page.items.find((t) => t.artwork_url);
          window.resonanceUI.setArtwork(img, art?.artwork_url);
          if (kind === "albums")
            info.textContent = [
              page.items[0]?.album_artist_credit ||
                page.items[0]?.artist_credit ||
                "Unknown artist",
              item.release_year,
            ]
              .filter(Boolean)
              .join(" · ");
        })
        .catch(() => {
          if (img.isConnected) img.dataset.pending = "false";
        });
    load(true);
  }
  async function queueCollection(path, play) {
    status.textContent = "Preparing the music…";
    const tracks = [];
    let next = null;
    do {
      const page = await readAPI(
        path + `?limit=200${next ? "&cursor=" + encodeURIComponent(next) : ""}`,
        undefined,
      );
      tracks.push(...page.items.filter((t) => t.available));
      next = page.next_cursor;
      if (tracks.length > 1000) throw new Error("Queue limit");
    } while (next);
    if (!tracks.length) {
      status.textContent = "No available tracks in this collection.";
      return;
    }
    await window.resonanceUser.addCollection(tracks, play ? "now" : "end");
  }
  async function search() {
    if (view !== "search") return;
    browseAbort.abort();
    browseAbort = new AbortController();
    const generation = ++navigationGeneration;
    const query = document.querySelector("#search-query").value.trim();
    items.replaceChildren();
    homeContent.replaceChildren();
    more.hidden = true;
    if (!query) {
      status.textContent = "";
      emptyState(
        "What would you like to hear?",
        "Search for a track, artist, or album in your own library.",
      );
      return;
    }
    status.textContent = "Searching…";
    skeleton();
    try {
      const result = await readAPI(
        "/api/v1/search?q=" + encodeURIComponent(query) + "&limit=20",
      );
      if (generation !== navigationGeneration) return;
      homeContent.replaceChildren();
      let count = 0;
      for (const kind of ["tracks", "artists", "albums"]) {
        if (!result[kind].length) continue;
        items.append(
          node("li", kind[0].toUpperCase() + kind.slice(1), "group-heading"),
        );
        for (const item of result[kind]) {
          count++;
          if (kind === "tracks") items.append(trackRow(item, count));
          else {
            const row = node("li", "", "track-row result-row");
            const img = artwork(
              item.artwork_url,
              "row-art",
              `${kind === "albums" ? "album" : "artist"}:${item.id}`,
            );
            const copy = node("div", "", "track-copy");
            copy.append(
              action(
                label(item, kind),
                () => openGroup(kind, item),
                "item-title",
              ),
              node(
                "span",
                groupCredit(item, kind),
                "item-subtitle cover-credit",
              ),
            );
            row.append(img, copy, window.resonanceUI.icon("caret-right"));
            img.dataset.kind = kind;
            img.dataset.id = item.id;
            if (item.track_count === undefined) {
              img.dataset.pending = "true";
              coverObserver.observe(img);
            }
            items.append(row);
          }
        }
      }
      status.textContent = count
        ? `${count} ${count === 1 ? "match" : "matches"} in your library`
        : "No results";
      if (!count)
        emptyState(
          "No matches this time.",
          "Try a shorter title, artist credit, or album name.",
        );
    } catch (error) {
      if (generation === navigationGeneration && error.name !== "AbortError") {
        homeContent.replaceChildren();
        status.textContent =
          "Search is unavailable. Check your connection and try again.";
      }
    }
  }
  function startTrack(track) {
    return window.resonanceUser.addQueue(track, "now");
  }
  function openPlaylist(id, updateURL = true) {
    beginView("playlists");
    document.querySelector("#collection-links").hidden = true;
    if (updateURL)
      history.pushState(null, "", `#playlists/${encodeURIComponent(id)}`);
    window.resonanceUser.openPlaylist(id);
  }
  function playTrack(track, selection = null) {
    if (!track.available) return;
    window.resonanceUser?.setPlayback(track, selection);
    playerError.hidden = true;
    nowTitle.textContent = track.title || "Untitled track";
    nowCredit.textContent = track.artist_credit || "Unknown artist";
    window.resonanceUI.setArtwork(
      cover,
      track.artwork_url,
      window.resonanceUI.artworkKey(track),
    );
    cover.hidden = false;
    window.dispatchEvent(new CustomEvent("resonance:track", { detail: track }));
    audio.src = track.stream_url;
    audio.play().catch(() => {
      playerError.textContent = audio.error
        ? "This audio is unavailable or cannot be decoded by this browser."
        : "Playback could not start. Try Play again, or choose another track.";
      playerError.hidden = false;
    });
  }
  cover.addEventListener("error", () => {
    if (cover.dataset.missing !== "true")
      window.resonanceUI.setArtwork(cover, null);
  });
  audio.addEventListener("error", () => {
    playerError.textContent =
      "This audio is unavailable or cannot be decoded by this browser. Try another track.";
    playerError.hidden = false;
  });
  document.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-view]");
    if (button) selectView(button.dataset.view);
  });
  document.querySelector(".brand").addEventListener("click", (event) => {
    event.preventDefault();
    selectView("home");
  });
  document.querySelector("#search-query").addEventListener("input", () => {
    document.querySelector("#clear-search").hidden =
      !document.querySelector("#search-query").value;
    browseAbort.abort();
    navigationGeneration++;
    items.replaceChildren();
    homeContent.replaceChildren();
    status.textContent = document.querySelector("#search-query").value.trim()
      ? "Searching…"
      : "";
    clearTimeout(searchTimer);
    searchTimer = setTimeout(search, 220);
  });
  document.querySelector("#clear-search").addEventListener("click", () => {
    const input = document.querySelector("#search-query");
    input.value = "";
    input.dispatchEvent(new Event("input"));
    input.focus();
  });
  document
    .querySelector("#catalog-order")
    .addEventListener("change", (event) => {
      catalogOrder = event.target.value;
      catalogPositions.delete(view);
      selectView(view, false);
    });
  document
    .querySelector("#available-only")
    .addEventListener("change", (event) => {
      availableOnly = event.target.checked;
      catalogPositions.delete(view);
      selectView(view, false);
    });

  items.addEventListener("keydown", (event) => {
    if (
      view !== "search" ||
      !["ArrowDown", "ArrowUp", "Home", "End", "Escape"].includes(event.key)
    )
      return;
    const results = [...items.querySelectorAll(".item-title:not(:disabled)")];
    if (event.key === "Escape") {
      event.preventDefault();
      document.querySelector("#search-query").focus();
      return;
    }
    const index = results.indexOf(event.target);
    if (index < 0) return;
    event.preventDefault();
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? results.length - 1
          : Math.max(
              0,
              Math.min(
                results.length - 1,
                index + (event.key === "ArrowDown" ? 1 : -1),
              ),
            );
    results[next]?.focus();
  });
  document.querySelector("#search-form").addEventListener("submit", (event) => {
    event.preventDefault();
    clearTimeout(searchTimer);
    search();
  });
  document
    .querySelector("#search-query")
    .addEventListener("keydown", (event) => {
      if (event.key === "ArrowDown") {
        const target = items.querySelector("button:not(:disabled)");
        if (target) {
          event.preventDefault();
          target.focus();
        }
      }
    });
  document.addEventListener("keydown", (event) => {
    if (
      event.key === "/" &&
      !event.ctrlKey &&
      !event.metaKey &&
      !event.altKey &&
      !event.target.closest("input,textarea,select,[contenteditable],dialog")
    ) {
      event.preventDefault();
      selectView("search");
    }
  });
  more.addEventListener("click", () => load());
  document
    .querySelector("#previous-page")
    .addEventListener("click", () => load(false, pageIndex - 1));
  document.querySelector("#refresh-view").addEventListener("click", () => {
    if (group)
      openGroup(
        group.kind,
        {
          id: group.id,
          display_title: title.textContent,
          display_credit: title.textContent,
        },
        false,
      );
    else selectView(view, false);
  });
  async function route() {
    const [kind, id] = location.hash.slice(1).split("/");
    if (id && kind === "playlists") {
      openPlaylist(id, false);
      return;
    }
    if (id && ["artists", "albums"].includes(kind)) {
      const observedHash = location.hash,
        observedGeneration = navigationGeneration;
      try {
        const item = await readAPI(`/api/v1/${kind}/${encodeURIComponent(id)}`);
        if (
          observedHash !== location.hash ||
          observedGeneration !== navigationGeneration
        )
          return;
        openGroup(kind, item, false);
      } catch (error) {
        if (error.name === "AbortError" || observedHash !== location.hash)
          return;
        selectView(kind, false);
      }
      return;
    }
    selectView(kind || "home", false);
  }
  window.addEventListener("popstate", route);
  window.resonanceBrowse = {
    playTrack,
    play: startTrack,
    openPlaylist,
    queueCollection,
    selectView,
    load,
    openGroup,
    trackRow,
    artwork,
    syncNavigation,
    watchArtwork(img, trackID) {
      window.resonanceUI.setArtwork(img, null, `track:${trackID}`);
      img.dataset.pending = "true";
      img.dataset.kind = "tracks";
      img.dataset.id = trackID;
      coverObserver.observe(img);
    },
    node,
    action,
  };
  // DOMContentLoaded waits for every deferred controller script. A zero-delay
  // timer can run while a later script is still downloading on a cold connection.
  if (document.readyState === "complete") route();
  else document.addEventListener("DOMContentLoaded", route, { once: true });
})();
