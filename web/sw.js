// Only this explicit list may enter Cache Storage. Never cache APIs, artwork,
// audio, Range requests, writes, diagnostic pages or host administration.
const VERSION = "resonance-shell-__ASSET_VERSION__";
const SHELL = [
  "/library.html",
  "/components.css",
  "/player.css",
  "/library.js",
  "/ui.js",
  "/icons.css",
  "/api.js",
  "/playback-modes.js",
  "/favorites-state.js",
  "/listening-state.js",
  "/context-menu.js",
  "/row-motion.js",
  "/listening-session.js",
  "/playlist-picker.js",
  "/playlist-destination.js",
  "/user-library.js",
  "/player.js",
  "/listening-summary.js",
  "/pwa.js",
  "/manifest.webmanifest",
  "/assets/icon-192-v2.png",
  "/assets/icon-512-v2.png",
  "/assets/artwork-unavailable.webp",
  "/assets/sleeve-meadow-v2.webp",
  "/assets/sleeve-tide-v2.webp",
  "/assets/sleeve-shadow-v2.webp",
  "/assets/sleeve-ridge-v2.webp",
  "/assets/sleeve-floral-v2.webp",
  "/assets/sleeve-room-v2.webp",
  "/assets/sleeve-terra-v2.webp",
  "/assets/sleeve-linen-v2.webp",
  "/assets/sleeve-leaf-v2.webp",
  "/assets/sleeve-dune-v2.webp",
  "/assets/resonance-mark.png",
  "/assets/fonts/cormorant-500.woff2",
  "/assets/fonts/cormorant-600.woff2",
  "/assets/fonts/cormorant-500-italic.woff2",
  "/assets/fonts/inter-400.woff2",
  "/assets/fonts/inter-500.woff2",
  "/assets/fonts/inter-600.woff2",
  "/assets/icons/arrow-down.svg",
  "/assets/icons/arrow-left.svg",
  "/assets/icons/arrow-up.svg",
  "/assets/icons/arrows-clockwise.svg",
  "/assets/icons/books.svg",
  "/assets/icons/books-fill.svg",
  "/assets/icons/house-fill.svg",
  "/assets/icons/magnifying-glass-fill.svg",
  "/assets/icons/caret-down.svg",
  "/assets/icons/caret-right.svg",
  "/assets/icons/check-circle.svg",
  "/assets/icons/clock-counter-clockwise.svg",
  "/assets/icons/dots-three.svg",
  "/assets/icons/download-simple.svg",
  "/assets/icons/folder.svg",
  "/assets/icons/heart.svg",
  "/assets/icons/heart-fill.svg",
  "/assets/icons/house.svg",
  "/assets/icons/list.svg",
  "/assets/icons/magnifying-glass.svg",
  "/assets/icons/music-notes.svg",
  "/assets/icons/pause.svg",
  "/assets/icons/pencil-simple.svg",
  "/assets/icons/play.svg",
  "/assets/icons/playlist.svg",
  "/assets/icons/plus.svg",
  "/assets/icons/queue.svg",
  "/assets/icons/shuffle.svg",
  "/assets/icons/repeat.svg",
  "/assets/icons/repeat-once.svg",
  "/assets/icons/skip-back.svg",
  "/assets/icons/skip-forward.svg",
  "/assets/icons/speaker-high.svg",
  "/assets/icons/trash.svg",
  "/assets/icons/warning-circle.svg",
  "/assets/icons/x.svg",
];
self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(VERSION).then((cache) => cache.addAll(SHELL)));
});
// Do not skipWaiting or claim clients: active audio must not be reloaded or
// switched to an incompatible shell while a newer version installs.
self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter(
              (key) => key.startsWith("resonance-shell-") && key !== VERSION,
            )
            .map((key) => caches.delete(key)),
        ),
      ),
  );
});
self.addEventListener("fetch", (event) => {
  const req = event.request,
    url = new URL(req.url);
  if (
    req.method !== "GET" ||
    req.headers.has("Range") ||
    url.origin !== self.location.origin
  )
    return;
  if (
    req.mode === "navigate" &&
    (url.pathname === "/" || url.pathname === "/library.html")
  ) {
    // A controlled document and its assets must come from one shell version.
    // A new worker waits until the old clients close, preserving active audio.
    event.respondWith(
      caches
        .open(VERSION)
        .then(
          async (cache) => (await cache.match("/library.html")) || fetch(req),
        ),
    );
    return;
  }
  if (!url.search && SHELL.includes(url.pathname))
    event.respondWith(
      caches
        .open(VERSION)
        .then(async (cache) => (await cache.match(url.pathname)) || fetch(req)),
    );
});
