// Actual 10k catalog: paging/context restoration and real unused History.
const { chromium } = require("@playwright/test");
const fs = require("node:fs/promises");
const path = require("node:path");
const base = process.env.RESONANCE_M2_SCALE_URL || "http://127.0.0.1:8107";
const out =
  process.env.RESONANCE_BROWSE_CONTEXT_DIR ||
  "data/m2/quality-escalation/resume3-large-catalog-context";
(async () => {
  await fs.mkdir(out, { recursive: true });
  const browser = await chromium.launch({ channel: "chrome" }),
    records = [];
  try {
    for (const width of [1440, 390]) {
      const context = await browser.newContext({
          viewport: { width, height: width === 390 ? 844 : 900 },
          serviceWorkers: "block",
        }),
        page = await context.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.goto(base + "/#albums");
      await page.locator("#items .cover-card").nth(49).waitFor();
      await page.locator("#more").click();
      await page.waitForFunction(() =>
        document.querySelector("#status").textContent.includes("page 2"),
      );
      const card = page.locator("#items .cover-card").first(),
        name = await card.getAttribute("aria-label"),
        originalID = await card.locator("img").getAttribute("data-id");
      await card.press("Enter");
      await page.getByRole("heading", { name, exact: true }).waitFor();
      await page
        .getByRole("button", { name: "Back to albums", exact: true })
        .press("Enter");
      await page.waitForFunction(() =>
        document.querySelector("#status").textContent.includes("page 2"),
      );
      const restored = await page
        .locator("#items .cover-card")
        .first()
        .getAttribute("aria-label");
      const restoredID = await page
        .locator("#items .cover-card")
        .first()
        .locator("img")
        .getAttribute("data-id");
      const focus = await page.evaluate(
        () => document.activeElement.querySelector("img")?.dataset.id,
      );
      if (
        restored !== name ||
        restoredID !== originalID ||
        focus !== originalID
      )
        throw Error("Real catalog page/focus was not restored");
      await page.evaluate(async () => document.fonts.ready);
      await page.screenshot({
        path: path.join(out, `catalog-return-${width}.png`),
      });
      records.push({
        width,
        original: name,
        originalID,
        restored,
        restoredID,
        focus,
        status: await page.locator("#status").textContent(),
      });
      const history = await (
        await context.request.get(base + "/api/v1/history?limit=50")
      ).json();
      if (history.items.length)
        throw Error("Scale fixture is no longer an unused listening history");
      await page.goto(base + "/#history");
      await page
        .getByRole("heading", {
          name: "Your listening, remembered.",
          exact: true,
        })
        .waitFor();
      await page.screenshot({
        path: path.join(out, `history-empty-${width}.png`),
      });
      await page
        .getByRole("button", { name: "Find a song", exact: true })
        .press("Enter");
      const searchFocused = await page
        .getByRole("searchbox")
        .evaluate((e) => e === document.activeElement);
      if (!searchFocused)
        throw Error("Real empty History cannot lead to keyboard Search");
      records.push({
        width,
        historyCount: history.items.length,
        searchFocused,
        errors,
      });
      await context.close();
    }
  } finally {
    await browser.close();
    await fs.writeFile(
      path.join(out, "context.json"),
      JSON.stringify(
        {
          date: new Date().toISOString(),
          definition:
            "Actual 10k rooted catalog; real API page 2, album detail and browser Back; no mocked data or response interception. Empty History verified from the real scale database.",
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
