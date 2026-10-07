// Read-only presentation state. Never substitutes a newer queue token for the
// token observed by the decoder; mutation authority remains in user-library.
(function (root) {
  function project(queue, playback) {
    const items = queue?.items || [];
    const selectedIndex = items.findIndex(
      (item) => item.id === queue?.current_item_id,
    );
    const selected = items[selectedIndex] || null;
    const selection = playback?.selection;
    const followsQueue = !!(
      selection &&
      queue?.selection_state === "selected" &&
      selection.itemID === queue.current_item_id &&
      selection.token === queue.selection_token
    );
    const mode = !playback?.track
      ? "ready"
      : followsQueue
        ? "queue"
        : playback.ended &&
            selection?.itemID === selected?.id &&
            queue?.selection_state === "stopped"
          ? "finished"
          : selection
            ? "detached"
            : "single";
    return {
      mode,
      followsQueue,
      selected,
      selectedIndex,
      previous: selectedIndex > 0 ? items.slice(0, selectedIndex) : [],
      upcoming: items.slice(selectedIndex < 0 ? 0 : selectedIndex + 1),
      explanation:
        mode === "queue"
          ? queue?.context
            ? `Playing from ${queue.context.name}. Up Next continues through ${queue.context.total} ${queue.context.total === 1 ? "song" : "songs"}.`
            : "Playing from your queue. Up Next continues automatically."
          : mode === "single"
            ? "Playing one song. Your saved queue stays ready to resume."
            : mode === "finished"
              ? "You reached the end of your queue. Replay this song or choose something new."
              : mode === "detached"
                ? "Your queue changed. This song is separate from it; resume the queue when you’re ready."
                : selected
                  ? "Your queue is saved. Resume it when you’re ready."
                  : "Choose a song, or build your queue.",
    };
  }
  function controls(queue, playback, prepared, position, repeat = "off") {
    const state = project(queue, playback);
    const ready =
      !playback?.track &&
      prepared &&
      queue?.selection_state === "selected" &&
      prepared.itemID === queue.current_item_id &&
      prepared.token === queue.selection_token;
    const finished = state.mode === "finished";
    const enabled = state.followsQueue || ready || finished;
    const any = queue?.items.some((item) => item.available) || false;
    return {
      previous: !!(
        enabled &&
        (state.previous.some((item) => item.available) ||
          (queue?.context?.previous && itemsUnderCap(queue)) ||
          (state.followsQueue && position > 3) ||
          (repeat === "all" &&
            any &&
            (!queue?.context || itemsUnderCap(queue))))
      ),
      next: !!(
        enabled &&
        (state.upcoming.some((item) => item.available) ||
          queue?.context?.more ||
          (repeat === "all" && any))
      ),
    };
  }
  function itemsUnderCap(queue) {
    return (queue?.items.length || 0) < 1000;
  }
  if (typeof module !== "undefined") module.exports = { project, controls };
  if (!root?.document) return;
  let queue = null,
    playback = null;
  let changing = false;
  const listeners = new Set();
  const get = () => ({
    queue,
    playback,
    changing,
    ...project(queue, playback),
  });
  const publish = () => {
    for (const listener of listeners) listener(get());
  };
  root.resonanceListening = {
    get,
    controls,
    setPlayback(value) {
      playback = value;
      publish();
    },
    subscribe(listener) {
      listeners.add(listener);
      listener(get());
      return () => listeners.delete(listener);
    },
  };
  root.addEventListener("resonance:queue", (event) => {
    queue = event.detail;
    publish();
  });
  root.addEventListener("resonance:modes", publish);
  root.addEventListener("resonance:queue-step", (event) => {
    changing = event.detail.pending;
    publish();
  });
  const audio = root.document.querySelector("#audio");
  for (const name of ["play", "pause", "ended", "emptied"])
    audio.addEventListener(name, () => {
      if (!playback) return;
      playback = { ...playback, ended: audio.ended };
      publish();
    });
})(typeof window === "undefined" ? null : window);
