// Player gain is separate from device master volume. The media element is the
// authority; all controls also follow changes made outside their input handlers.
(() => {
  const audio = document.getElementById("audio");
  const key = "resonance-player-volume-v1";
  const sliders = ["volume", "full-volume"].map((id) =>
    document.getElementById(id),
  );
  const buttons = ["mute-player", "full-mute"].map((id) =>
    document.getElementById(id),
  );
  let lastPositive = 1;
  const original = audio.volume;
  audio.volume = 0.5;
  const adjustable = Math.abs(audio.volume - 0.5) < 0.001;
  audio.volume = original;
  try {
    const saved = JSON.parse(localStorage.getItem(key));
    if (
      saved &&
      Number.isFinite(saved.volume) &&
      saved.volume >= 0 &&
      saved.volume <= 1
    ) {
      if (adjustable) audio.volume = saved.volume;
      audio.muted = saved.muted === true;
      if (
        Number.isFinite(saved.lastPositive) &&
        saved.lastPositive > 0 &&
        saved.lastPositive <= 1
      )
        lastPositive = saved.lastPositive;
    }
  } catch {
    /* Storage denial does not disable playback. */
  }
  function render() {
    const silent = audio.muted || (adjustable && audio.volume === 0);
    if (adjustable && audio.volume > 0) lastPositive = audio.volume;
    for (const slider of sliders) {
      slider.hidden = !adjustable;
      slider.value = silent ? 0 : audio.volume;
      slider.setAttribute(
        "aria-valuetext",
        silent
          ? "Muted"
          : `${Math.round(audio.volume * 100)} percent player volume`,
      );
    }
    for (const button of buttons) {
      window.resonanceUI.button(
        button,
        silent
          ? "speaker-slash"
          : audio.volume <= 0.5
            ? "speaker-low"
            : "speaker-high",
        silent ? "Unmute player" : "Mute player",
        true,
      );
      button.setAttribute("aria-pressed", String(silent));
      button.title = silent
        ? "Unmute player"
        : "Mute player · device volume is separate";
    }
    document.getElementById("volume-note").textContent = adjustable
      ? "Player volume · device volume is separate"
      : "Use your device buttons to change volume";
    try {
      localStorage.setItem(
        key,
        JSON.stringify({
          volume: audio.volume,
          muted: audio.muted,
          lastPositive,
        }),
      );
    } catch {
      /* Optional local preference. */
    }
  }
  for (const button of buttons)
    button.addEventListener("click", () => {
      if (audio.muted || (adjustable && audio.volume === 0)) {
        if (adjustable && audio.volume === 0) audio.volume = lastPositive;
        audio.muted = false;
      } else audio.muted = true;
      render();
    });
  for (const slider of sliders)
    slider.addEventListener("input", () => {
      if (!adjustable) return;
      audio.volume = Number(slider.value);
      audio.muted = audio.volume === 0;
      render();
    });
  audio.addEventListener("volumechange", render);
  render();
})();
