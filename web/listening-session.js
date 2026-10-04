// Owns actual listening sessions, measured listening time and durable retries.
// UI controllers receive explicit callbacks; they never own session clocks.
window.resonanceCreateListeningSession = ({
  audio: userAudio,
  error: userError,
  failure: userFailure,
  getQueue,
  refreshQueue,
  onQueueChanged,
  selectedQueuePlayback,
}) => {
  const userAPI = window.resonanceAPI.read;
  const userWrite = window.resonanceAPI.write;
  const { pendingMutations, removePendingMutation } = window.resonanceAPI;
  let currentPlayback = null;
  let lastClock = performance.now();
  let stalled = false;
  let starting = false;
  let ending = false;
  const instanceID =
    sessionStorage.getItem("resonance.instance") || window.resonanceID();
  sessionStorage.setItem("resonance.instance", instanceID);
  function setPlayback(track, selection) {
    if (currentPlayback?.session) sendReport("stopped");
    currentPlayback = { track, selection, session: null, decoderFailed: false };
    window.resonanceListening.setPlayback({ track, selection });
    lastClock = performance.now();
  }
  function accrue() {
    const now = performance.now();
    if (
      currentPlayback?.session &&
      !userAudio.paused &&
      !userAudio.seeking &&
      !stalled &&
      userAudio.readyState >= 3
    )
      currentPlayback.session.listenedMS += Math.min(
        2000,
        Math.max(0, now - lastClock),
      );
    lastClock = now;
  }
  function reportBody(reason) {
    const session = currentPlayback.session;
    const duration =
      Number.isFinite(userAudio.duration) && userAudio.duration > 0
        ? Math.round(userAudio.duration * 1000)
        : null;
    return {
      sequence: ++session.sequence,
      listened_ms: Math.round(session.listenedMS),
      position_ms: Math.max(0, Math.round(userAudio.currentTime * 1000)),
      duration_ms: duration,
      seek_count: session.seekCount,
      ...(reason ? { terminal_reason: reason } : {}),
    };
  }
  async function sendReport(reason = null) {
    accrue();
    if (!currentPlayback?.session) return;
    const session = currentPlayback.session;
    const report = reportBody(reason);
    const pending = JSON.stringify({ id: session.id, report });
    sessionStorage.setItem("resonance.pending_report", pending);
    try {
      await userWrite(
        "PUT",
        `/api/v1/listening-sessions/${session.id}/report`,
        report,
        false,
      );
      if (sessionStorage.getItem("resonance.pending_report") === pending)
        sessionStorage.removeItem("resonance.pending_report");
      if (reason && currentPlayback?.session === session)
        currentPlayback.session = null;
      return true;
    } catch (error) {
      if (reason) userFailure(error);
      return false;
    }
  }
  async function startSession() {
    if (
      starting ||
      !currentPlayback ||
      currentPlayback.session ||
      currentPlayback.historyUnavailable ||
      performance.now() < (currentPlayback.historyRetryAt || 0) ||
      userAudio.paused ||
      userAudio.currentTime <= 0.1
    )
      return;
    starting = true;
    const playback = currentPlayback;
    try {
      const started = await userWrite(
        "POST",
        "/api/v1/listening-sessions",
        {
          id: window.resonanceID(),
          track_id: playback.track.id,
          client_instance_id: instanceID,
          ...(playback.selection
            ? {
                queue_item_id: playback.selection.itemID,
                selection_token: playback.selection.token,
              }
            : {}),
        },
        false,
      );
      if (currentPlayback === playback && !userAudio.error) {
        playback.session = {
          id: started.id,
          sequence: 0,
          listenedMS: 0,
          seekCount: 0,
        };
        lastClock = performance.now();
      }
    } catch (error) {
      if (currentPlayback !== playback) return;
      if (error.status >= 400 && error.status < 500)
        playback.historyUnavailable = true;
      else playback.historyRetryAt = performance.now() + 5000;
      userError.textContent =
        "Audio is playing, but listening history could not start.";
      userError.hidden = false;
    } finally {
      starting = false;
    }
  }
  async function ended() {
    accrue();
    const playback = currentPlayback;
    if (!playback?.session) return;
    if (!playback.selection) {
      const saved = await sendReport("ended");
      if (
        saved &&
        currentPlayback === playback &&
        window.resonancePlaybackModes.get().repeat !== "off"
      )
        window.resonanceBrowse.playTrack(playback.track, null);
      return;
    }
    const q = getQueue() || (await refreshQueue());
    const request = {
      direction: "ended",
      repeat: window.resonancePlaybackModes.get().repeat,
      expected_version: q.revision,
      expected_current_item_id: playback.selection.itemID,
      selection_token: playback.selection.token,
      session_id: playback.session.id,
      final_report: reportBody("ended"),
    };
    const pending = { key: window.resonanceID(), request };
    sessionStorage.removeItem("resonance.pending_report");
    sessionStorage.setItem("resonance.pending_ended", JSON.stringify(pending));
    try {
      const change = await postPendingEnded(pending);
      sessionStorage.removeItem("resonance.pending_ended");
      playback.session = null;
      await refreshQueue();
      await onQueueChanged();
      if (
        currentPlayback === playback &&
        change?.selection_state === "selected" &&
        change.current_item_id &&
        change.selection_token
      )
        await selectedQueuePlayback({
          itemID: change.current_item_id,
          token: change.selection_token,
        });
    } catch (error) {
      if (error.code === "stale_selection") {
        sessionStorage.removeItem("resonance.pending_ended");
        // Learn the changed queue without adopting its selection authority.
        // Otherwise boundary checks still see the obsolete one-song snapshot.
        try {
          await refreshQueue();
          await onQueueChanged();
        } catch {}
      }
      userError.textContent =
        error.code === "stale_selection"
          ? "Queue selection changed in another tab. This track did not advance the queue."
          : "Queue advance was not saved. Retry after the server recovers.";
      userError.hidden = false;
    }
  }
  async function postPendingEnded(pending) {
    const send = (item) =>
      userAPI("/api/v1/queue/advance", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": item.key,
        },
        body: JSON.stringify(item.request),
      });
    try {
      return await send(pending);
    } catch (error) {
      if (error.code !== "stale_version") throw error;
      const q = await userAPI("/api/v1/queue");
      if (
        q.selection_state !== "selected" ||
        q.current_item_id !== pending.request.expected_current_item_id ||
        q.selection_token !== pending.request.selection_token
      ) {
        throw { code: "stale_selection", status: 409 };
      }
      const rebased = {
        key: window.resonanceID(),
        request: { ...pending.request, expected_version: q.revision },
      };
      sessionStorage.setItem(
        "resonance.pending_ended",
        JSON.stringify(rebased),
      );
      return send(rebased);
    }
  }
  async function retryPending() {
    for (const [name, path, method] of [
      ["resonance.pending_ended", "/api/v1/queue/advance", "POST"],
      ["resonance.pending_report", null, "PUT"],
    ]) {
      const raw = sessionStorage.getItem(name);
      if (!raw) continue;
      try {
        const pending = JSON.parse(raw);
        if (name === "resonance.pending_ended") await postPendingEnded(pending);
        else
          await userAPI(
            path || `/api/v1/listening-sessions/${pending.id}/report`,
            {
              method,
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(pending.report),
            },
          );
        sessionStorage.removeItem(name);
      } catch (error) {
        if (error.status >= 400 && error.status < 500)
          sessionStorage.removeItem(name);
      }
    }
    for (const pending of pendingMutations()) {
      try {
        await userAPI(pending.path, {
          method: pending.method,
          headers: {
            "Content-Type": "application/json",
            "Idempotency-Key": pending.key,
          },
          body: pending.body,
        });
        removePendingMutation(pending.identity, pending.key);
      } catch (error) {
        if (error.status >= 400 && error.status < 500)
          removePendingMutation(pending.identity, pending.key);
      }
    }
    try {
      await refreshQueue();
    } catch {
      /* Durable queue reads remain available after recovery. */
    }
  }

  userAudio.addEventListener("timeupdate", () => {
    accrue();
    startSession();
  });
  userAudio.addEventListener("seeking", () => {
    if (currentPlayback?.session) currentPlayback.session.seekCount++;
    accrue();
  });
  userAudio.addEventListener("seeked", () => {
    if (currentPlayback?.session) sendReport();
  });
  userAudio.addEventListener("waiting", () => {
    stalled = true;
    accrue();
  });
  userAudio.addEventListener("playing", () => {
    stalled = false;
    lastClock = performance.now();
  });
  userAudio.addEventListener("pause", accrue);
  userAudio.addEventListener("ended", async () => {
    if (ending) return;
    ending = true;
    try {
      await ended();
    } catch (error) {
      userFailure(error);
    } finally {
      ending = false;
    }
  });
  userAudio.addEventListener("error", () => {
    if (currentPlayback) currentPlayback.decoderFailed = true;
    if (currentPlayback?.session) sendReport("decoder_error");
  });
  setInterval(() => {
    accrue();
    if (
      currentPlayback?.session &&
      !userAudio.paused &&
      currentPlayback.session.listenedMS >=
        (currentPlayback.session.lastSentMS || 0) + 10000
    ) {
      currentPlayback.session.lastSentMS = currentPlayback.session.listenedMS;
      sendReport();
    }
  }, 500);
  window.addEventListener("pagehide", () => {
    if (currentPlayback?.session) sendReport("disconnected");
  });

  return {
    current: () => currentPlayback,
    setPlayback,
    accrue,
    sendReport,
    retryPending,
  };
};
