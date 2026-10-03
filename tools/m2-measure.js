const { chromium } = require("@playwright/test");
const fs = require("node:fs");
const os = require("node:os");
const { performance } = require("node:perf_hooks");
const base = process.env.RESONANCE_M2_SCALE_URL || "http://127.0.0.1:8092";
const out =
  process.env.RESONANCE_M2_MEASUREMENT || "docs/benchmarks/M2-raw.json";
const percentile = (values, p) =>
  [...values].sort((a, b) => a - b)[
    Math.max(0, Math.ceil(values.length * p) - 1)
  ];
let capturedResult;
(async () => {
  const result = (capturedResult = {
    date: new Date().toISOString(),
    command: "node tools/m2-measure.js",
    environment: {
      platform: os.platform(),
      os: os.release(),
      cpu: os.cpus()[0].model,
      logicalCPUs: os.cpus().length,
      memoryBytes: os.totalmem(),
      node: process.version,
    },
    dataset: {
      generator: "cmd/m17corpus",
      seed: 20260928,
      tracks: 10000,
      artists: 100,
      albums: 1000,
    },
    conditions:
      "Loopback HTTP, warm filesystem/database caches after import; synthetic short MP3s, not a playback SLO.",
    requests: [],
    browser: [],
  });
  let cursor = null,
    count = 0;
  do {
    const response = await fetch(
      base +
        "/api/v1/tracks?limit=200" +
        (cursor ? "&cursor=" + encodeURIComponent(cursor) : ""),
    );
    if (!response.ok) throw Error("catalog unavailable");
    const page = await response.json();
    count += page.items.length;
    cursor = page.next_cursor;
  } while (cursor);
  if (count !== 10000) throw Error(`Expected 10000 tracks, got ${count}`);
  const queries = [
    "Scale Track 00000",
    "Scale Artist 042",
    "Scale Album 0042",
    "Scale",
    "no such track",
    "%_",
  ];
  for (let sample = 0; sample < 100; sample++)
    for (const query of queries) {
      const start = performance.now();
      const response = await fetch(
        base + "/api/v1/search?q=" + encodeURIComponent(query) + "&limit=20",
      );
      const body = await response.json();
      const durationMS = performance.now() - start;
      if (response.status !== 200) throw Error("search failed");
      if (Object.values(body).some((list) => list.length > 20))
        throw Error("unbounded response");
      result.requests.push({
        kind: "search",
        query,
        sample,
        status: response.status,
        durationMS,
        counts: {
          tracks: body.tracks.length,
          artists: body.artists.length,
          albums: body.albums.length,
        },
      });
    }
  for (let sample = 0; sample < 100; sample++) {
    const start = performance.now();
    const response = await fetch(base + "/api/v1/tracks?limit=50");
    const body = await response.json();
    if (response.status !== 200 || body.items.length !== 50)
      throw Error("browse failed");
    result.requests.push({
      kind: "catalog",
      sample,
      status: response.status,
      durationMS: performance.now() - start,
    });
  }
  const browser = await chromium.launch({ channel: "chrome", headless: true });
  try {
    for (const width of [390, 1440]) {
      const page = await browser.newPage({
        viewport: { width, height: 900 },
        serviceWorkers: "block",
      });
      const start = performance.now();
      await page.goto(base + "/#tracks");
      await page.locator("#items li").nth(49).waitFor();
      const firstRenderMS = performance.now() - start;
      let largest = 0;
      for (let i = 0; i < 6; i++) {
        const before = await page.locator("#items").textContent();
        await page.locator("#more").click();
        await page.waitForFunction(
          (previous) =>
            document.querySelector("#items").textContent !== previous,
          before,
        );
        largest = Math.max(largest, await page.locator("#items li").count());
      }
      const searchStart = performance.now();
      await page.goto(base + "/#search");
      await page.getByRole("searchbox").fill("Scale Artist 042");
      await page.waitForFunction(() =>
        document.querySelector("#status").textContent.includes("matches"),
      );
      result.browser.push({
        width,
        firstRenderMS,
        searchInteractionMS: performance.now() - searchStart,
        maximumRenderedTracks: largest,
        domElements: await page.locator("*").count(),
        horizontalOverflow: await page.evaluate(
          () => document.documentElement.scrollWidth > innerWidth,
        ),
      });
      await page.close();
    }
  } finally {
    await browser.close();
  }
  result.summary = {};
  for (const kind of ["search", "catalog"]) {
    const times = result.requests
      .filter((r) => r.kind === kind)
      .map((r) => r.durationMS);
    result.summary[kind] = {
      samples: times.length,
      p50: percentile(times, 0.5),
      p95: percentile(times, 0.95),
      p99: percentile(times, 0.99),
    };
  }
  fs.mkdirSync(require("node:path").dirname(out), { recursive: true });
  fs.writeFileSync(out, JSON.stringify(result, null, 2));
  console.log(
    JSON.stringify(
      { summary: result.summary, browser: result.browser },
      null,
      2,
    ),
  );
})().catch((error) => {
  console.error(error);
  if (capturedResult) {
    capturedResult.failure = { message: error.message, stack: error.stack };
    fs.writeFileSync(
      out.replace(/\.json$/, ".failed.json"),
      JSON.stringify(capturedResult, null, 2),
    );
  }
  process.exit(1);
});
