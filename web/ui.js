// Small presentation helpers. Icons are unmodified, licensed Phosphor assets.
(() => {
  const fallback = "/assets/artwork-unavailable.webp";
  function icon(name) {
    const span = document.createElement("span");
    span.className = `icon icon-${name}`;
    span.setAttribute("aria-hidden", "true");
    return span;
  }
  function button(button, name, label, iconOnly = false) {
    button.replaceChildren(icon(name));
    const text = document.createElement("span");
    text.className = "button-label";
    text.textContent = label;
    button.append(text);
    button.classList.add("icon-button");
    button.classList.toggle("icon-only", iconOnly);
    button.setAttribute("aria-label", label);
    button.title = label;
    return button;
  }
  const sleeves = [
    "meadow-v2",
    "tide-v2",
    "shadow-v2",
    "ridge-v2",
    "floral-v2",
    "terra-v2",
    "linen-v2",
    "leaf-v2",
    "room-v2",
    "dune-v2",
  ];
  function artworkKey(track) {
    return track.album_id
      ? `album:${track.album_id}`
      : `track:${track.id || track.track_id}`;
  }
  function setArtwork(img, url, key = img.dataset.sleeveKey || "") {
    img.dataset.sleeveKey = key;
    let hash = 2166136261;
    for (const char of key)
      hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
    const sleeve = key ? sleeves[(hash >>> 0) % sleeves.length] : "clay";
    img.dataset.sleeve = url ? "original" : sleeve;
    img.src =
      url || (sleeve === "clay" ? fallback : `/assets/sleeve-${sleeve}.webp`);
    img.dataset.missing = String(!url);
    img.dataset.pending = "false";
    if (img.id === "full-art") {
      const ambient = document.querySelector("#full-ambient");
      if (ambient) ambient.src = img.getAttribute("src");
    }
    img.title = "";
  }
  // A playlist belongs to the listener, so its quiet cover is its own signal
  // mark rather than an invented album photograph. Stable ID controls rhythm.
  function collectionMark(name, id) {
    let seed = 2166136261;
    for (const char of id)
      seed = Math.imul(seed ^ char.charCodeAt(0), 16777619);
    const mark = document.createElement("span");
    mark.className = "playlist-monogram";
    mark.setAttribute("aria-hidden", "true");
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 180 180");
    svg.setAttribute("focusable", "false");
    const amplitude = 8 + ((seed >>> 0) % 17);
    const period = 38 + ((seed >>> 5) % 22);
    for (const y of [28, 48, 132, 152]) {
      const path = document.createElementNS(svg.namespaceURI, "path");
      path.setAttribute(
        "d",
        `M -24 ${y} q ${period / 2} ${-amplitude} ${period} 0 t ${period} 0 t ${period} 0 t ${period} 0 t ${period} 0 t ${period} 0`,
      );
      svg.append(path);
    }
    const initial = document.createElement("span");
    initial.textContent = Array.from(name.trim())[0]?.toUpperCase() || "♪";
    mark.append(svg, initial);
    return mark;
  }
  window.resonanceUI = {
    icon,
    button,
    fallback,
    setArtwork,
    artworkKey,
    collectionMark,
  };
  // Occasional dialogs get a short entrance. Closing remains immediate and
  // neither keyboard use nor reduced-motion preferences pay an animation cost.
  const animations = new WeakMap();
  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
  for (const dialog of document.querySelectorAll("dialog")) {
    new MutationObserver(() => {
      animations.get(dialog)?.cancel();
      if (
        dialog.id === "now-playing" ||
        !dialog.open ||
        reducedMotion.matches ||
        document.body.dataset.input === "keyboard"
      )
        return;
      animations.set(
        dialog,
        dialog.animate(
          [
            { opacity: 0, transform: "translateY(8px) scale(0.99)" },
            { opacity: 1, transform: "none" },
          ],
          { duration: 220, easing: "cubic-bezier(0.23, 1, 0.32, 1)" },
        ),
      );
    }).observe(dialog, { attributes: true, attributeFilter: ["open"] });
  }
  reducedMotion.addEventListener("change", () => {
    if (reducedMotion.matches)
      for (const dialog of document.querySelectorAll("dialog"))
        animations.get(dialog)?.cancel();
  });
  document.addEventListener(
    "keydown",
    () => {
      document.body.dataset.input = "keyboard";
      for (const dialog of document.querySelectorAll("dialog"))
        animations.get(dialog)?.cancel();
      window.dispatchEvent(new Event("resonance:cancel-motion"));
    },
    true,
  );
  document.addEventListener(
    "pointerdown",
    () => {
      document.body.dataset.input = "pointer";
    },
    true,
  );
  const audio = document.querySelector("#audio");
  let currentTrackID = null;
  function markCurrentTrack() {
    for (const row of document.querySelectorAll(".track-row[data-track-id]")) {
      const current = row.dataset.trackId === currentTrackID;
      row.classList.toggle("is-current", current);
      const mark = row.querySelector(".playback-mark");
      if (!mark) continue;
      mark.hidden = !current;
      if (current)
        mark.textContent = audio.error
          ? "Unavailable"
          : audio.ended
            ? "Finished"
            : audio.readyState === 0
              ? "Loading"
              : audio.paused
                ? "Paused"
                : audio.readyState < 3
                  ? "Buffering"
                  : "Playing";
    }
  }
  window.addEventListener("resonance:track", (event) => {
    currentTrackID = event.detail.id;
    markCurrentTrack();
  });
  for (const event of [
    "play",
    "pause",
    "playing",
    "waiting",
    "emptied",
    "error",
    "ended",
    "loadedmetadata",
  ])
    audio.addEventListener(event, markCurrentTrack);
  new MutationObserver(markCurrentTrack).observe(
    document.querySelector("#items"),
    { childList: true },
  );
  for (const element of document.querySelectorAll("[data-icon]"))
    button(
      element,
      element.dataset.icon,
      element.getAttribute("aria-label") || element.textContent.trim(),
      element.dataset.iconOnly === "true",
    );
})();
