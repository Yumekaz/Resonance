// Actual UI frame pacing; no video or screenshot capture during measurement.
const { chromium } = require("@playwright/test");
const fs = require("node:fs/promises");
const base = process.env.RESONANCE_E2E_BASE_URL || "http://127.0.0.1:8095";
const out =
  process.env.RESONANCE_QUALITY_FRAME_FILE ||
  "data/m2/quality-escalation/resume2-frames.json";
(async () => {
  const browser = await chromium.launch({ channel: "chrome" });
  const records = [];
  try {
    for (const width of [390, 1440])
      for (const rate of [1, 4]) {
        const context = await browser.newContext({
          viewport: { width, height: width === 390 ? 844 : 900 },
          serviceWorkers: "block",
        });
        const page = await context.newPage();
        const cdp = await context.newCDPSession(page);
        await cdp.send("Emulation.setCPUThrottlingRate", { rate });
        await page.goto(base + "/#tracks");
        const row = page.locator("#items .track-row").filter({
          has: page.getByRole("button", { name: "long", exact: true }),
        });
        await row.getByRole("button", { name: /^More actions for/ }).click();
        await page
          .getByRole("menuitem", { name: "Play this song only", exact: true })
          .click();
        await page.waitForFunction(
          () => document.querySelector("#audio").currentTime > 0.2,
        );
        await page.evaluate(() => {
          window.qualityFrames = [];
          let last;
          function frame(now) {
            if (last !== undefined)
              window.qualityFrames.push({
                interval: now - last,
                active:
                  document.querySelector("#now-playing").dataset
                    .transitioning === "true",
              });
            last = now;
            window.qualityFrameHandle = requestAnimationFrame(frame);
          }
          window.qualityFrameHandle = requestAnimationFrame(frame);
        });
        for (let cycle = 0; cycle < 5; cycle++) {
          await page.locator("#open-player").click();
          await page.waitForFunction(
            () =>
              document.querySelector("#now-playing").dataset.transitioning !==
              "true",
          );
          await page.locator("#close-player").click();
          await page.waitForFunction(
            () =>
              !document.querySelector("#now-playing").open &&
              document.querySelector("#now-playing").dataset.transitioning !==
                "true",
          );
        }
        const frames = await page.evaluate(() => {
          cancelAnimationFrame(window.qualityFrameHandle);
          return window.qualityFrames;
        });
        const active = frames
          .filter((f) => f.active)
          .map((f) => f.interval)
          .sort((a, b) => a - b);
        const percentile = (p) =>
          active[Math.max(0, Math.ceil(active.length * p) - 1)];
        records.push({
          width,
          cpuRate: rate,
          frames,
          activeCount: active.length,
          activeP50: percentile(0.5),
          activeP95: percentile(0.95),
          activeP99: percentile(0.99),
          activeOver33ms: active.filter((f) => f > 33.4).length,
          audio: await page
            .locator("#audio")
            .evaluate((a) => ({ paused: a.paused, time: a.currentTime })),
        });
        await context.close();
      }
  } finally {
    await browser.close();
  }
  await fs.writeFile(
    out,
    JSON.stringify(
      {
        date: new Date().toISOString(),
        definition:
          "Five pointer open/close cycles per viewport/profile; rAF interval samples while transition flag active; headless desktop Chrome, emulated CPU throttle, warm local server, no recording/screenshots. This is pacing evidence, not proof of physical-device smoothness or compositor FPS.",
        records,
      },
      null,
      2,
    ),
  );
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
