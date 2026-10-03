(() => {
  let installPrompt = null,
    connectionGeneration = 0;
  const badge = document.querySelector("#connection-state"),
    notice = document.querySelector("#unreachable");
  const updateBadge = document.querySelector("#update-state");
  let shellState = window.isSecureContext ? "checking" : "unavailable";
  let updateReady = false;
  function showUpdate() {
    updateReady = true;
    if (!updateBadge) return;
    updateBadge.hidden = false;
    updateBadge.textContent =
      "Update saved · close all Resonance tabs to use it";
  }
  async function connectivity() {
    const generation = ++connectionGeneration;
    try {
      const response = await fetch("/ready", {
        cache: "no-store",
        signal: AbortSignal.timeout(5000),
      });
      if (!response.ok) throw Error();
      if (generation !== connectionGeneration) return;
      const recovered = !notice.hidden;
      badge.textContent = "Library connected";
      notice.hidden = true;
      if (recovered) {
        window.resonanceUser?.retryPending();
        document.querySelector("#refresh-view").click();
      }
    } catch {
      if (generation === connectionGeneration) {
        badge.textContent = "Connection unavailable";
        notice.hidden = false;
      }
    }
  }
  document
    .querySelector("#retry-connection")
    .addEventListener("click", async () => {
      await connectivity();
      if (notice.hidden) {
        window.resonanceUser?.retryPending();
        document.querySelector("#refresh-view").click();
      }
    });
  window.addEventListener("online", connectivity);
  window.addEventListener("offline", connectivity);
  setInterval(connectivity, 30000);
  connectivity();
  if ("serviceWorker" in navigator && window.isSecureContext)
    navigator.serviceWorker
      .register("/sw.js")
      .then((registration) => {
        shellState = registration.active ? "available" : "checking";
        if (registration.waiting) showUpdate();
        navigator.serviceWorker.ready.then(() => {
          shellState = "available";
        });
        const watch = (worker) =>
          worker?.addEventListener("statechange", () => {
            if (
              worker.state === "installed" &&
              navigator.serviceWorker.controller
            )
              showUpdate();
            if (worker.state === "redundant" && !registration.active)
              shellState = "unavailable";
          });
        watch(registration.installing);
        registration.addEventListener("updatefound", () => {
          watch(registration.installing);
        });
      })
      .catch(() => {
        shellState = "unavailable";
      });
  window.addEventListener("beforeinstallprompt", (event) => {
    event.preventDefault();
    installPrompt = event;
    document.querySelector("#install-app").hidden = false;
  });
  window.addEventListener("appinstalled", () => {
    installPrompt = null;
    document.querySelector("#install-app").hidden = true;
  });
  document.querySelector("#install-app").addEventListener("click", async () => {
    if (installPrompt) {
      await installPrompt.prompt();
      installPrompt = null;
      document.querySelector("#install-app").hidden = true;
    }
  });
  document.querySelector("#app-info").addEventListener("click", () => {
    const { content } = window.resonanceUser.openActionDialog(
      "Resonance on this device",
    );
    content.append(
      window.resonanceBrowse.node(
        "p",
        "Resonance plays music from your personal library. Music needs a connection to your server.",
      ),
    );
    content.append(
      window.resonanceBrowse.node(
        "p",
        matchMedia("(display-mode: standalone)").matches
          ? "Installed on this device. Music still needs your server."
          : installPrompt
            ? "Installation is available. Choose Install Resonance from App options."
            : !window.isSecureContext
              ? "PWA installation and offline app caching are unavailable on this HTTP connection. You can listen in this browser; its menu may still offer a Home Screen shortcut."
              : "This browser has not offered an install prompt. If supported, use its Install app or Add to Home Screen menu. Availability depends on this browser and device.",
      ),
    );
    content.append(
      window.resonanceBrowse.node(
        "p",
        shellState === "available"
          ? "App shell saved. You can reopen the interface without a connection; songs and library data are never saved offline."
          : shellState === "checking"
            ? "Checking whether this browser can save the app shell."
            : "Offline app shell unavailable on this connection or browser.",
      ),
    );
    content.append(
      window.resonanceBrowse.node(
        "p",
        notice.hidden
          ? "Your library server is connected."
          : "Your server is unavailable. Check its address, your network, and whether Resonance is running.",
      ),
    );
    if (updateReady)
      content.append(
        window.resonanceBrowse.node(
          "p",
          "An update is saved and waiting. Close every Resonance tab and installed app window, then reopen. Your current listening is never interrupted by an update.",
        ),
      );
  });
  document.querySelector("#mobile-options").addEventListener("click", () => {
    const { dialog, content } =
      window.resonanceUser.openActionDialog("Resonance");
    content.append(
      window.resonanceBrowse.action("About this app", () =>
        document.querySelector("#app-info").click(),
      ),
    );
    if (installPrompt)
      content.append(
        window.resonanceBrowse.action("Install Resonance", () => {
          dialog.close();
          document.querySelector("#install-app").click();
        }),
      );
  });
})();
