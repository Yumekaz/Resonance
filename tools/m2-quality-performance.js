const { chromium } = require("@playwright/test");
const fs = require("node:fs/promises");
const os = require("node:os");
const base = process.env.RESONANCE_M2_SCALE_URL || "http://127.0.0.1:8107";
const output =
  process.env.RESONANCE_QUALITY_PERFORMANCE_OUTPUT ||
  "data/m2/quality-escalation/scale-stressed-browser.json";
const rank = (a, p) =>
  [...a].sort((x, y) => x - y)[Math.ceil(a.length * p) - 1];
(async () => {
  const browser = await chromium.launch({ channel: "chrome" }),
    samples = [];
  for (const profile of [
    { name: "fresh-process-context", rate: 1, latency: 0 },
    { name: "4x-cpu-80ms-network", rate: 4, latency: 80 },
  ])
    for (const width of [390, 1440])
      for (let sample = 0; sample < 5; sample++) {
        const context = await browser.newContext({
            viewport: { width, height: 900 },
            serviceWorkers: "block",
          }),
          page = await context.newPage();
        const cdp = await context.newCDPSession(page);
        await cdp.send("Emulation.setCPUThrottlingRate", {
          rate: profile.rate,
        });
        await cdp.send("Network.enable");
        await cdp.send("Network.setCacheDisabled", { cacheDisabled: true });
        if (profile.latency)
          await cdp.send("Network.emulateNetworkConditions", {
            offline: false,
            latency: profile.latency,
            downloadThroughput: 2 * 1024 * 1024,
            uploadThroughput: 1024 * 1024,
          });
        const started = performance.now();
        await page.goto(base + "/#tracks");
        await page.locator("#items .track-row").nth(49).waitFor();
        const readyMS = performance.now() - started;
        await page.getByRole("button", { name: "Search", exact: true }).click();
        const searchStarted = performance.now();
        await page.getByRole("searchbox").fill("Scale Track 0000");
        await page.waitForFunction(
          () =>
            document.querySelector("#items .track-row") &&
            !/Searching/.test(document.querySelector("#status").textContent),
        );
        const searchMS = performance.now() - searchStarted;
        const albumsStarted = performance.now();
        await page.locator('.sidebar [data-view="library"]').click();
        await page.waitForFunction(
          () =>
            document.querySelector("#items .cover-card") &&
            !/Loading/.test(document.querySelector("#status").textContent),
        );
        samples.push({
          profile: profile.name,
          width,
          sample,
          readyMS,
          searchMS,
          albumsMS: performance.now() - albumsStarted,
          layout: await page.evaluate(() => ({
            rows: document.querySelectorAll("#items > li").length,
            overflow: document.documentElement.scrollWidth > innerWidth,
            resources: performance.getEntriesByType("resource").map((r) => ({
              path: new URL(r.name).pathname,
              durationMS: r.duration,
              bytes: r.transferSize,
            })),
          })),
        });
        await context.close();
      }
  await browser.close();
  const summary = [];
  for (const profile of new Set(samples.map((s) => s.profile)))
    for (const width of [390, 1440]) {
      const group = samples.filter(
        (s) => s.profile === profile && s.width === width,
      );
      summary.push({
        profile,
        width,
        samples: group.length,
        readyP50: rank(
          group.map((s) => s.readyMS),
          0.5,
        ),
        readyP95: rank(
          group.map((s) => s.readyMS),
          0.95,
        ),
        searchP95: rank(
          group.map((s) => s.searchMS),
          0.95,
        ),
        albumsP95: rank(
          group.map((s) => s.albumsMS),
          0.95,
        ),
      });
    }
  await fs.writeFile(
    output,
    JSON.stringify(
      {
        date: new Date().toISOString(),
        command: "node tools/m2-quality-performance.js",
        base,
        datasetTracks: 10000,
        environment: {
          cpu: os.cpus()[0].model,
          logicalCPUs: os.cpus().length,
          memoryBytes: os.totalmem(),
          node: process.version,
          os: os.release(),
        },
        conditions:
          "Fresh Chrome contexts, browser cache disabled, service workers blocked, warm database/filesystem. Stress profile emulates CPU and network; it is not physical Android evidence. Search includes input debounce. Album readiness requires 50 bounded cards, not completion of every image.",
        summary,
        samples,
      },
      null,
      2,
    ),
  );
  console.log(JSON.stringify(summary));
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
