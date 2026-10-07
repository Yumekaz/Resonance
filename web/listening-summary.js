(() => {
  const host = document.querySelector("#listening-summary");
  const audio = document.querySelector("#audio");
  let lastSignature = "";
  function render(state) {
    const modes = window.resonancePlaybackModes.get();
    const source = state.queue?.context?.name || "your queue";
    document.querySelector("#listening-context").textContent = state.changing
      ? "Changing song…"
      : state.mode === "queue"
        ? modes.repeat === "one"
          ? `From ${source} · repeating this song`
          : modes.continuous || modes.repeat === "all"
            ? `From ${source} · keeps playing`
            : `From ${source} · ends after the last song`
        : state.mode === "single"
          ? state.queue?.items.length
            ? "One song · your queue is saved"
            : "Playing one song"
          : state.mode === "finished"
            ? "End of your queue · ready to replay"
            : state.mode === "detached"
              ? "This song · queue changed"
              : "Your saved queue · ready when you are";
    const signature = JSON.stringify([
      state.mode,
      state.playback?.track?.id,
      state.selected?.id,
      state.queue?.selection_token,
      state.queue?.context?.id,
      audio.paused,
      state.changing,
      modes.continuous,
      modes.repeat,
    ]);
    if (signature === lastSignature) return;
    lastSignature = signature;
    host.replaceChildren();
    const { node, artwork, action } = window.resonanceBrowse;
    const card = node("div", "", "listening-card");
    const track = state.playback?.track;
    if (track) {
      card.append(
        artwork(
          track.artwork_url,
          "summary-art",
          window.resonanceUI.artworkKey(track),
        ),
      );
      const copy = node("div", "", "summary-copy");
      copy.append(
        node("p", "NOW PLAYING", "eyebrow"),
        node("h2", track.title || "Untitled track"),
        node("p", track.artist_credit || "Unknown artist", "item-subtitle"),
        node(
          "p",
          state.mode === "queue"
            ? `Playing from ${source}.`
            : state.explanation,
          "hint",
        ),
      );
      const openPlayer = action("Open player", () =>
        document.querySelector("#open-player").click(),
      );
      openPlayer.classList.add("open-player");
      window.resonanceUI.button(openPlayer, "caret-right", "Open player");
      card.append(copy, openPlayer);
    } else {
      card.append(
        window.resonanceUI.icon("queue"),
        node("p", state.explanation),
      );
    }
    if (track || (!state.selected && state.queue?.items.length))
      host.append(card);
    if (!state.followsQueue && state.selected) {
      const saved = node("div", "", "saved-queue");
      const copy = node("div", "", "summary-copy");
      copy.append(
        node("strong", "Your saved queue"),
        node(
          "p",
          `Continue with ${state.selected.title || "Untitled track"}`,
          "item-subtitle",
        ),
      );
      const resume = action(
        "Resume queue",
        async () => {
          if (state.queue.selection_state === "selected")
            await window.resonanceUser.resumeSelection({
              itemID: state.selected.id,
              token: state.queue.selection_token,
            });
          else
            await window.resonanceUser.selectQueueItem(
              state.queue,
              state.selected.id,
            );
        },
        "primary",
      );
      resume.disabled = !state.selected.available;
      if (resume.disabled)
        copy.append(
          node(
            "p",
            "Choose Play now on a song below to start the queue.",
            "hint",
          ),
        );
      saved.append(copy, resume);
      host.append(saved);
    }
  }
  window.resonanceListening.subscribe(render);
  for (const event of ["play", "pause", "ended"])
    audio.addEventListener(event, () =>
      render(window.resonanceListening.get()),
    );
})();
