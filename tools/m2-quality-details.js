// Actual rooted catalog and disposable real playlists, never screenshot mocks.
const { chromium } = require("@playwright/test");
const fs = require("node:fs/promises");
const path = require("node:path");
const crypto = require("node:crypto");
const base = process.env.RESONANCE_E2E_BASE_URL || "http://127.0.0.1:8095";
const out =
  process.env.RESONANCE_QUALITY_DETAIL_DIR ||
  "data/m2/quality-escalation/resume2-details";
(async () => {
  await fs.mkdir(out, { recursive: true });
  const browser = await chromium.launch({ channel: "chrome" });
  const records = [];
  try {
    for (const width of [320, 390, 1440]) {
      const context = await browser.newContext({
        viewport: {
          width,
          height: width === 320 ? 568 : width === 390 ? 844 : 900,
        },
        serviceWorkers: "block",
      });
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      const capture = async (name) => {
        await page.evaluate(async () =>
          Promise.all(
            document
              .getAnimations()
              .filter((a) => a.effect?.getTiming().iterations !== Infinity)
              .map((a) => a.finished.catch(() => {})),
          ),
        );
        await page.evaluate(async () => {
          await document.fonts.ready;
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
          accessibility: await page.locator("body").ariaSnapshot(),
          geometry: await page.evaluate(() => ({
            width: innerWidth,
            documentWidth: document.documentElement.scrollWidth,
            focused:
              document.activeElement?.getAttribute("aria-label") ||
              document.activeElement?.id,
            styles: [...document.styleSheets].map(
              (s) => new URL(s.href).pathname,
            ),
          })),
        });
      };
      await page.goto(base + "/#tracks");
      await page
        .getByRole("button", { name: "Browser Song", exact: true })
        .waitFor();
      await capture("track-tools");
      await page.goto(base + "/#albums");
      await page
        .getByRole("button", { name: "Browser Album", exact: true })
        .click();
      await page
        .getByRole("button", { name: "Play album", exact: true })
        .waitFor();
      await capture("album-detail");
      await page.goto(base + "/#artists");
      await page
        .getByRole("button", { name: "Browser Artist", exact: true })
        .click();
      await page
        .getByRole("heading", { name: "Browser Artist", exact: true })
        .waitFor();
      await capture("artist-detail");
      await page.goto(base + "/#search");
      await page.getByRole("searchbox").fill("Shared");
      await page.waitForFunction(() =>
        document.querySelector("#status").textContent.includes("matches"),
      );
      await capture("ambiguous-search");
      const row = page.locator("#items .track-row").first();
      await row.getByRole("button", { name: /^More actions for/ }).click();
      await capture("context-menu");
      await page.keyboard.press("Escape");
      await page.getByRole("searchbox").fill("no such song in this fixture");
      await page
        .getByRole("heading", { name: "No matches this time.", exact: true })
        .waitFor();
      await capture("no-results");
      await page.goto(base + "/#playlists");
      await page
        .getByRole("button", { name: "New playlist", exact: true })
        .click();
      await capture("create-playlist");
      await page.keyboard.press("Escape");
      const name =
        "雨の音 — A very long playlist name for testing honest wrapping and Unicode in a personal collection " +
        crypto.randomUUID().slice(0, 8);
      const response = await context.request.post(base + "/api/v1/playlists", {
        headers: { "Idempotency-Key": crypto.randomUUID() },
        data: { name, expected_version: 0 },
      });
      if (response.status() !== 201)
        throw new Error("Could not create disposable review playlist");
      const playlist = await response.json();
      try {
        await page.goto(base + "/#playlists/" + playlist.id);
        await page.getByRole("heading", { name, exact: true }).waitFor();
        await capture("empty-long-playlist");
        await page
          .getByRole("button", { name: "Add songs", exact: true })
          .click();
        await page
          .getByLabel("Find a song in your library", { exact: true })
          .fill("Browser Song");
        await page.locator(".picker-results button").first().click();
        await page.keyboard.press("Escape");
        await page.locator("#items .track-row").waitFor();
        await page.evaluate(() => window.scrollTo(0, 0));
        await capture("playlist-detail");
      } finally {
        const detail = await (
          await context.request.get(base + "/api/v1/playlists/" + playlist.id)
        ).json();
        await context.request.delete(
          base + "/api/v1/playlists/" + playlist.id,
          { data: { expected_version: detail.revision } },
        );
      }
      await page.reload();
      await page
        .getByRole("heading", { name: "Playlist unavailable", exact: true })
        .waitFor();
      await capture("playlist-unavailable");
      records.push({ width, errors });
      await context.close();
    }
    const hostContext = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });
    const host = await hostContext.newPage();
    await host.goto("http://127.0.0.1:8081/");
    await host.locator("#listener-address a").waitFor();
    await host.screenshot({ path: path.join(out, "host-1440.png") });
    records.push({
      name: "host",
      accessibility: await host.locator("body").ariaSnapshot(),
    });
    await hostContext.close();
  } finally {
    await browser.close();
    await fs.writeFile(
      path.join(out, "details.json"),
      JSON.stringify(
        {
          date: new Date().toISOString(),
          definition:
            "Actual local catalog, disposable real API playlists; no mocked presentation data. Host screenshot contains test-only local folder paths and remains private raw evidence.",
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
