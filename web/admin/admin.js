(() => {
  const el = (id) => document.getElementById(id),
    message = el("message");
  let runtime = {},
    after = null,
    verifying = null,
    busy = false;
  let suggestedFolderName = null;
  let pickerRequest = null,
    pickerGeneration = 0,
    pickerAvailable = false;
  const node = (tag, text, className) => {
    const e = document.createElement(tag);
    e.textContent = text;
    if (className) e.className = className;
    return e;
  };
  const explanations = {
    folder_overlap:
      "This folder overlaps one already enrolled. Use the existing folder.",
    folder_unavailable:
      "The folder could not be opened. Check the drive, path and access on this computer.",
    verification_required:
      "Verify the folder identity before enabling it or scanning.",
    folder_quarantined:
      "The folder changed. Inspect the folder on this computer, then verify its identity.",
    folder_changed:
      "This path now points to a different folder. Inspect it before verifying.",
    identity_unavailable:
      "This filesystem cannot provide the identity evidence required for safe scans.",
    folder_disabled: "Enable this folder before scanning.",
    scan_running:
      "A scan is already running. Wait for it to finish and try again.",
    invalid_request: "Check the folder name and full local path.",
    operation_unavailable:
      "The operation could not finish. Check server readiness and try again.",
    catalog_unavailable:
      "The catalog is unavailable. Restore the database and refresh status.",
    host_only: "Host management is only available directly on this computer.",
    folder_picker_unavailable:
      "The folder picker is unavailable here. Enter a local path manually.",
    folder_picker_busy:
      "A folder picker is already open on this computer. Finish that selection first.",
    folder_picker_timeout:
      "The folder picker timed out. Choose folder to try again.",
    folder_picker_failed:
      "The folder picker could not finish. Try again or enter the path manually.",
  };
  async function api(path, body, signal) {
    const response = await fetch("/api/v1/admin/" + path, {
      cache: "no-store",
      signal,
      ...(body
        ? {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              "X-Resonance-Admin": "1",
            },
            body: JSON.stringify(body),
          }
        : {}),
    });
    const data = await response.json();
    if (!response.ok)
      throw Error(
        explanations[data.error?.code] ||
          "The operation could not finish. Refresh status and try again.",
      );
    return data;
  }
  function button(text, fn) {
    const b = node("button", text);
    b.type = "button";
    b.disabled = busy;
    b.addEventListener("click", async () => {
      if (busy) return;
      setBusy(true);
      try {
        await fn();
      } catch (error) {
        message.textContent = error.message;
      } finally {
        setBusy(false);
      }
    });
    return b;
  }
  function setBusy(value) {
    busy = value;
    // A refresh can replace the original button while its request is pending.
    // Newly rendered controls must not look enabled and silently ignore clicks.
    document.querySelectorAll("button").forEach((button) => {
      button.disabled = value;
    });
    if (pickerRequest) el("cancel-add").disabled = false;
    updateFolderChoice();
  }
  function updateFolderChoice() {
    const path = el("folder-path").value.trim();
    el("chosen-folder").textContent = path;
    el("chosen-folder").hidden = !path;
    el("confirm-add-folder").disabled = busy || !path;
  }
  async function refresh(reset = true) {
    const state = await api("status");
    pickerAvailable = state.folder_picker?.available === true;
    el("choose-folder").hidden = !pickerAvailable;
    if (!pickerAvailable) {
      el("manual-folder").open = true;
      el("picker-status").textContent =
        "Enter a local folder path on this server.";
    }
    runtime = state.library || {};
    el("listener-address").replaceChildren();
    if (state.listener) {
      const text =
        state.listener.mode === "local"
          ? "The listener is available on this computer only. Phone listening needs a listener configured for your local network."
          : "The listener accepts local network connections. Open it from a phone on the same Wi-Fi as this computer.";
      el("listener-address").append(node("p", text, "hint"));
      if (state.listener.url) {
        const link = node("a", "Open your music listener", "listener-link");
        link.href = state.listener.url;
        link.target = "_blank";
        link.rel = "noopener";
        el("listener-address").append(
          link,
          node("p", state.listener.url, "hint"),
        );
      }
    }
    el("server-status").replaceChildren();
    for (const [name, value] of [
      ["Process", state.process === "running" ? "Running" : "Unavailable"],
      [
        "Music catalog",
        state.catalog === "ready"
          ? "Ready to listen"
          : "Unavailable — check database",
      ],
      [
        "Automatic updates",
        runtime.watcher_state === "running"
          ? "Watching for changes"
          : "Reduced coverage — scans still recover changes",
      ],
      ["Pending folders", String(runtime.dirty_roots || 0)],
    ])
      el("server-status").append(node("dt", name), node("dd", value));
    if (reset) {
      after = null;
      el("roots").replaceChildren();
    }
    const page = await api(
      "roots" + (after ? "?after=" + encodeURIComponent(after) : ""),
    );
    for (const root of page.items) renderRoot(root);
    if (reset && !page.items.length) el("setup-guide").open = true;
    after = page.next;
    el("more-roots").hidden = !after;
    if (!el("roots").children.length)
      el("roots").append(
        node(
          "p",
          "Your library is waiting. Add a music folder to get started.",
          "hint",
        ),
      );
  }
  function renderRoot(root) {
    const state = runtime.roots?.find((s) => s.id === root.id) || {};
    const box = node("article", "", "folder");
    const top = node("div", "", "folder-top");
    top.append(
      node("h3", root.name),
      node(
        "span",
        `${root.enabled ? "Enabled" : "Disabled"} · ${root.verification_state === "verified" ? "Verified" : root.verification_state === "quarantined" ? "Needs attention" : "Not verified"}`,
        "state",
      ),
    );
    box.append(top, node("p", root.path, "folder-path"));
    const note =
      root.verification_state !== "verified"
        ? "Inspect this folder and verify its identity before scanning."
        : !root.enabled
          ? "Hidden from Library and Search. Saved playlist entries are retained; enable this folder to listen again."
          : state.last_scan_error_code
            ? "A scan needs attention. Check this drive and try Scan now."
            : state.dirty
              ? "Changes are waiting to be scanned."
              : state.watch_state === "watching"
                ? "Automatic folder updates are active."
                : "Automatic coverage is limited. Scan now to check for changes.";
    box.append(node("p", note, "hint"));
    if (state.last_scan_at)
      box.append(
        node(
          "p",
          `Last scan: ${new Date(state.last_scan_at).toLocaleString()} · ${state.last_scan_status || "unknown"}`,
          "hint",
        ),
      );
    if (state.retry_at)
      box.append(
        node(
          "p",
          "Next retry: " + new Date(state.retry_at).toLocaleTimeString(),
          "hint",
        ),
      );
    const actions = node("div", "", "actions");
    actions.append(
      button("Verify", () => {
        verifying = root;
        el("verify-path").textContent = root.path;
        el("verify-dialog").showModal();
      }),
      button(root.enabled ? "Disable" : "Enable", async () => {
        await api(
          `roots/${root.id}/${root.enabled ? "disable" : "enable"}`,
          {},
        );
        message.textContent =
          "Folder updated. Automatic status will refresh shortly.";
        await refresh();
      }),
      button("Scan now", async () => {
        message.textContent =
          "Scanning this folder. This may take a few minutes.";
        const scan = await api(`roots/${root.id}/scan`, {});
        message.textContent = `Scan ${scan.status}: ${scan.files_visited} files checked, ${scan.imported} imported, ${scan.files_unchanged || 0} unchanged, ${scan.skipped || 0} skipped, ${scan.failed} could not be read. Supported music: MP3, FLAC and WAV.`;
        await refresh();
      }),
    );
    box.append(actions);
    el("roots").append(box);
  }
  el("show-add").addEventListener("click", () => {
    el("add-folder").hidden = false;
    (pickerAvailable ? el("choose-folder") : el("folder-name")).focus();
    updateFolderChoice();
  });
  el("folder-path").addEventListener("input", updateFolderChoice);
  el("folder-name").addEventListener("input", () => {
    suggestedFolderName = null;
  });
  el("choose-folder").addEventListener("click", async () => {
    if (busy) return;
    const generation = ++pickerGeneration;
    const request = new AbortController();
    pickerRequest = request;
    setBusy(true);
    el("choose-folder").setAttribute("aria-busy", "true");
    el("picker-status").textContent =
      "Choose a folder in the Windows window, or Cancel to return here.";
    try {
      const choice = await api("folder-picker", {}, request.signal);
      if (generation !== pickerGeneration || el("add-folder").hidden) return;
      if (choice.cancelled) {
        el("picker-status").textContent =
          "Selection cancelled. Your chosen folder has not changed.";
        return;
      }
      el("folder-path").value = choice.path;
      if (
        !el("folder-name").value.trim() ||
        el("folder-name").value === suggestedFolderName
      ) {
        let name = "",
          bytes = 0;
        const encoder = new TextEncoder();
        for (const char of choice.name) {
          const length = encoder.encode(char).length;
          if (bytes + length > 128) break;
          name += char;
          bytes += length;
        }
        el("folder-name").value = name;
        suggestedFolderName = name;
      }
      updateFolderChoice();
      el("picker-status").textContent =
        "Folder selected. Confirm the name, then choose Add folder.";
    } catch (error) {
      if (error.name !== "AbortError" && generation === pickerGeneration)
        el("picker-status").textContent = error.message;
    } finally {
      if (pickerRequest === request) pickerRequest = null;
      el("choose-folder").removeAttribute("aria-busy");
      setBusy(false);
      if (!el("add-folder").hidden) el("choose-folder").focus();
    }
  });
  el("cancel-add").addEventListener("click", () => {
    pickerGeneration++;
    pickerRequest?.abort();
    el("picker-status").textContent =
      "Choose a folder to continue. Your previous selection has not changed.";
    el("add-folder").hidden = true;
    el("show-add").focus();
  });
  el("add-folder").addEventListener("submit", async (event) => {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    try {
      await api("roots", {
        name: el("folder-name").value,
        path: el("folder-path").value,
      });
      event.target.reset();
      suggestedFolderName = null;
      updateFolderChoice();
      event.target.hidden = true;
      message.textContent = "Folder added. Use Scan now to import your music.";
      await refresh();
    } catch (error) {
      message.textContent = error.message;
    } finally {
      setBusy(false);
    }
  });
  el("confirm-verify").addEventListener("click", async () => {
    if (!verifying || busy) return;
    setBusy(true);
    try {
      await api(`roots/${verifying.id}/verify`, { confirm: true });
      el("verify-dialog").close();
      message.textContent = "Folder identity verified.";
      await refresh();
    } catch (error) {
      message.textContent = error.message;
      el("verify-dialog").close();
    } finally {
      setBusy(false);
    }
  });
  el("cancel-verify").addEventListener("click", () =>
    el("verify-dialog").close(),
  );
  async function refreshAction(reset) {
    if (busy) return;
    setBusy(true);
    try {
      await refresh(reset);
    } catch (error) {
      message.textContent = error.message;
    } finally {
      setBusy(false);
    }
  }
  el("refresh").addEventListener("click", () => refreshAction(true));
  el("more-roots").addEventListener("click", () => refreshAction(false));
  refresh().catch((error) => {
    message.textContent = error.message;
  });
})();
