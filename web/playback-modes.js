// Preferences are local to this browser. Queue order remains server-authoritative;
// repeat intent travels with the fenced, receipt-protected advance operation.
(function (root) {
  function shuffled(values, random = Math.random) {
    const result = [...values];
    for (let i = result.length - 1; i > 0; i--) {
      const j = Math.floor(random() * (i + 1));
      [result[i], result[j]] = [result[j], result[i]];
    }
    if (result.length > 1 && result.every((v, i) => v === values[i]))
      [result[0], result[1]] = [result[1], result[0]];
    return result;
  }
  function upcomingOrder(queue, random) {
    const at = queue.items.findIndex(
      (item) => item.id === queue.current_item_id,
    );
    const ids = queue.items.map((item) => item.id);
    return [...ids.slice(0, at + 1), ...shuffled(ids.slice(at + 1), random)];
  }
  function restoredOrder(queue, original) {
    const at = queue.items.findIndex(
      (item) => item.id === queue.current_item_id,
    );
    const ids = queue.items.map((item) => item.id);
    const rank = new Map(original.map((id, index) => [id, index]));
    const known = ids
      .slice(at + 1)
      .filter((id) => rank.has(id))
      .sort((a, b) => rank.get(a) - rank.get(b));
    let index = 0;
    // Explicit additions such as Play next keep their slots. Deleted items
    // are absent; duplicate songs remain distinct queue occurrence IDs.
    return ids.map((id, i) => (i > at && rank.has(id) ? known[index++] : id));
  }
  function advanceOptions(state, direction) {
    const repeat =
      state.repeat === "one" && direction === "ended"
        ? "one"
        : state.repeat === "all" || state.continuous
          ? "all"
          : "off";
    return {
      repeat,
      reshuffle:
        direction !== "previous" && repeat === "all" && state.shuffle === true,
    };
  }
  if (typeof module !== "undefined")
    module.exports = { shuffled, upcomingOrder, restoredOrder, advanceOptions };
  if (!root?.document) return;
  const key = "resonance.playback_modes";
  let state = { shuffle: false, repeat: "off", continuous: true, original: [] };
  function read() {
    try {
      const saved = JSON.parse(localStorage.getItem(key));
      if (saved && ["off", "one", "all"].includes(saved.repeat))
        state = {
          shuffle: saved.shuffle === true,
          repeat: saved.repeat,
          continuous: saved.continuous !== false,
          original: Array.isArray(saved.original)
            ? saved.original
                .filter((id) => typeof id === "string")
                .slice(0, 1000)
            : [],
        };
    } catch {
      /* Storage unavailable: modes still work during this session. */
    }
  }
  read();
  function publish() {
    root.dispatchEvent(
      new CustomEvent("resonance:modes", { detail: { ...state } }),
    );
  }
  function save(value) {
    state = { ...state, ...value };
    try {
      localStorage.setItem(key, JSON.stringify(state));
    } catch {}
    publish();
  }
  root.resonancePlaybackModes = {
    get: () => ({ ...state, original: [...state.original] }),
    setShuffle: (shuffle, original = []) => save({ shuffle, original }),
    setContinuous: (continuous) => save({ continuous }),
    advanceOptions: (direction) => advanceOptions(state, direction),
    cycleRepeat: () =>
      save({
        repeat:
          state.repeat === "off"
            ? "all"
            : state.repeat === "all"
              ? "one"
              : "off",
      }),
    shuffled,
    upcomingOrder,
    restoredOrder,
  };
  root.addEventListener("storage", (event) => {
    if (event.key === key) {
      read();
      publish();
    }
  });
})(typeof window === "undefined" ? null : window);
