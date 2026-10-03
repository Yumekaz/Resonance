const { chromium } = require("@playwright/test");
const fs = require("node:fs/promises");
const path = require("node:path");
(async () => {
  const base = process.env.RESONANCE_E2E_BASE_URL || "http://127.0.0.1:8095";
  const directory =
    process.env.RESONANCE_QUALITY_CAPTURE_DIR ||
    "data/m2/quality-escalation/screens-first";
  await fs.mkdir(directory, { recursive: true });
  const browser = await chromium.launch({ channel: "chrome" });
  const evidence = [];
  async function capture(page, name) {
    await page.evaluate(async () => {
      await document.fonts.ready;
      await Promise.all(
        [...document.querySelectorAll("img")]
          .filter((i) => i.getBoundingClientRect().top < innerHeight)
          .map((i) => i.decode().catch(() => {})),
      );
    });
    await page.screenshot({
      path: path.join(directory, name + ".png"),
      fullPage: false,
    });
    evidence.push({
      name,
      url: page.url(),
      viewport: page.viewportSize(),
      layout: await page.evaluate(() => ({
        width: innerWidth,
        height: innerHeight,
        documentWidth: document.documentElement.scrollWidth,
        items: document.querySelectorAll("#items .track-row").length,
        player: document.querySelector("#now-playing").open
          ? document
              .querySelector(".full-controls")
              .getBoundingClientRect()
              .toJSON()
          : null,
        styles: [...document.styleSheets].map((s) => new URL(s.href).pathname),
      })),
    });
  }
  for (const size of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 430, height: 932 },
    { width: 320, height: 568 },
    { width: 844, height: 390 },
    { width: 768, height: 1024 },
  ]) {
    const context = await browser.newContext({
      viewport: size,
      serviceWorkers: "block",
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    const views = [1440, 390].includes(size.width)
      ? [
          "home",
          "albums",
          "tracks",
          "search",
          "queue",
          "playlists",
          "favorites",
          "history",
        ]
      : ["albums"];
    for (const view of views) {
      await page.goto(base + "/#" + view);
      await page.waitForFunction(
        () =>
          document.querySelector("#connection-state").textContent ===
            "Library connected" &&
          !/Loading/.test(document.querySelector("#status").textContent),
      );
      if (view === "search") {
        await page.getByRole("searchbox").fill("Browser");
        await page.waitForFunction(
          () =>
            !/Searching/.test(document.querySelector("#status").textContent) &&
            document.querySelector("#items .item-title"),
        );
      }
      await page.waitForFunction(() =>
        [...document.querySelectorAll("#items img")]
          .filter((i) => i.getBoundingClientRect().top < innerHeight)
          .every((i) => i.dataset.pending !== "true"),
      );
      await capture(page, `${view}-${size.width}x${size.height}`);
    }
    await page.goto(base + "/#tracks");
    const soloRow = page
      .locator("#items .track-row")
      .filter({ has: page.getByRole("button", { name: "long", exact: true }) });
    await soloRow.getByRole("button", { name: /^More actions for/ }).click();
    await page
      .getByRole("menuitem", { name: "Play this song only", exact: true })
      .click();
    await page.waitForFunction(
      () => document.querySelector("#audio").currentTime > 0.1,
    );
    await page.locator("#open-player").click();
    await page.waitForFunction(
      () =>
        document.querySelector("#now-playing").open &&
        document.querySelector("#now-playing").dataset.transitioning !== "true",
    );
    await page.evaluate(async () =>
      Promise.all(
        document.getAnimations().map((a) => a.finished.catch(() => {})),
      ),
    );
    await capture(page, `player-${size.width}x${size.height}`);
    evidence.push({ viewport: size, errors });
    await context.close();
  }
  await fs.writeFile(
    path.join(directory, "capture-evidence.json"),
    JSON.stringify({ date: new Date().toISOString(), base, evidence }, null, 2),
  );
  await browser.close();
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
