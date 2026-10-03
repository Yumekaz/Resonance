// Real current listener: empty queue -> catalog play -> Play next -> player.
const { chromium } = require("@playwright/test");
const fs = require("node:fs/promises");
const path = require("node:path");
const crypto = require("node:crypto");
const base = process.env.RESONANCE_E2E_BASE_URL || "http://127.0.0.1:8095";
const out =
  process.env.RESONANCE_QUEUE_FLOW_DIR ||
  "data/m2/quality-escalation/resume3-queue-flow";
(async () => {
  await fs.mkdir(out, { recursive: true });
  const browser = await chromium.launch({ channel: "chrome" });
  const records = [];
  try {
    for (const width of [1440, 390]) {
      const context = await browser.newContext({
        viewport: { width, height: width === 390 ? 844 : 900 },
        serviceWorkers: "block",
      });
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      const q = await (
        await context.request.get(base + "/api/v1/queue")
      ).json();
      const cleared = await context.request.delete(base + "/api/v1/queue", {
        headers: { "Idempotency-Key": crypto.randomUUID() },
        data: { expected_version: q.revision },
      });
      if (!cleared.ok()) throw Error("Disposable review queue clear failed");
      async function capture(name) {
        await page.evaluate(async () => {
          await document.fonts.ready;
          await Promise.all(
            document
              .getAnimations()
              .filter((a) => a.effect?.getTiming().iterations !== Infinity)
              .map((a) => a.finished.catch(() => {})),
          );
          await Promise.all(
            [...document.querySelectorAll("img")]
              .filter((i) => i.getBoundingClientRect().top < innerHeight)
              .map((i) => i.decode().catch(() => {})),
          );
        });
        await page.screenshot({ path: path.join(out, `${name}-${width}.png`) });
        records.push({
          name,
          width,
          url: page.url(),
          state: await page.evaluate(() => ({
            title: document.querySelector("#now-title").textContent,
            source: document.querySelector("#listening-context").textContent,
            audioTime: document.querySelector("#audio").currentTime,
            paused: document.querySelector("#audio").paused,
            queueControlsHidden:
              document.querySelector("#queue-controls").hidden,
            dialogPalette:
              document.querySelector("#action-dialog").dataset.playerContext,
            width: innerWidth,
            documentWidth: document.documentElement.scrollWidth,
          })),
          accessibility: await page.locator("body").ariaSnapshot(),
        });
      }
      await page.goto(base + "/#queue");
      await page
        .getByRole("heading", {
          name: "Your next song starts here.",
          exact: true,
        })
        .waitFor();
      await capture("01-empty");
      await page
        .getByRole("button", { name: "Find a song", exact: true })
        .click();
      await page.getByRole("searchbox").fill("long");
      await page.getByRole("button", { name: "long", exact: true }).click();
      await page.waitForFunction(
        () =>
          document.querySelector("#audio").currentTime > 0.1 &&
          !document.querySelector("#audio").paused,
      );
      await page.getByRole("searchbox").fill("Browser Song");
      const row = page.locator("#items .track-row").filter({
        has: page.getByRole("button", { name: "Browser Song", exact: true }),
      });
      await row.getByRole("button", { name: /^More actions for/ }).click();
      await page
        .getByRole("menuitem", { name: "Play next", exact: true })
        .click();
      await page.locator("#open-queue").click();
      await page.locator("#items .track-row").nth(1).waitFor();
      await capture("02-playing-up-next");
      await page.locator("#open-player").click();
      await page.waitForFunction(
        () =>
          document.querySelector("#now-playing").open &&
          document.querySelector("#now-playing").dataset.transitioning !==
            "true" &&
          !/Loading/.test(
            document.querySelector("#full-queue-status").textContent,
          ),
      );
      await capture("03-player-queue");
      await page.locator("#full-playlist").click();
      await page.locator("#action-dialog").waitFor({ state: "visible" });
      await capture("04-player-playlist-destination");
      await page.keyboard.press("Escape");
      await page.locator("#full-queue").click();
      await page
        .locator("#items .track-row")
        .nth(1)
        .getByRole("button", { name: /^More actions for/ })
        .click();
      await capture("05-queue-actions");
      await page.keyboard.press("Escape");
      const actual = await (
        await context.request.get(base + "/api/v1/queue")
      ).json();
      if (
        actual.items.length !== 2 ||
        actual.items.find((i) => i.id === actual.current_item_id)?.title !==
          "long" ||
        !actual.items.some((i) => i.title === "Browser Song")
      )
        throw Error("Actual queue does not match the captured listening flow");
      await page.locator("#play-toggle").click();
      records.push({
        width,
        errors,
        queue: {
          items: actual.items.map((i) => ({
            id: i.id,
            title: i.title,
            position: i.position,
          })),
          current: actual.current_item_id,
        },
      });
      await context.close();
    }
  } finally {
    await browser.close();
    await fs.writeFile(
      path.join(out, "flow.json"),
      JSON.stringify(
        {
          date: new Date().toISOString(),
          definition:
            "Actual real fixture catalog, receipt-protected disposable review queue, visible Play and Play next actions, continuing actual audio. No mocked presentation data.",
          records,
        },
        null,
        2,
      ),
    );
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
