const { test, expect } = require("@playwright/test");
test.skip(!process.env.RESONANCE_M2_E2E, "Requires the real M2 server");
test.use({ serviceWorkers: "block" });
async function playLong(page) {
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "long", exact: true }).click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await expect
    .poll(() =>
      page.locator("#cover").evaluate((i) => i.complete && i.naturalWidth > 0),
    )
    .toBe(true);
}
for (const width of [390, 1440])
  test(`artwork expansion preserves audio and focus at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await playLong(page);
    const before = await page
      .locator("#audio")
      .evaluate((a) => ({ src: a.currentSrc, time: a.currentTime }));
    await page.locator("#open-player").click();
    await expect(page.locator("#now-playing")).toBeVisible();
    await page.waitForFunction(
      () =>
        document.querySelector("#now-playing").dataset.transitioning !== "true",
    );
    expect(await page.locator("#audio").evaluate((a) => a.currentSrc)).toBe(
      before.src,
    );
    await expect
      .poll(() => page.locator("#audio").evaluate((a) => a.currentTime))
      .toBeGreaterThan(before.time + 0.1);
    expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
    await page.locator("#close-player").click();
    await expect(page.locator("#now-playing")).not.toBeVisible();
    await page.waitForFunction(
      () =>
        document.querySelector("#now-playing").dataset.transitioning !== "true",
    );
    await expect(page.locator("#open-player")).toBeFocused();
    expect(await page.locator("#audio").evaluate((a) => a.currentSrc)).toBe(
      before.src,
    );
    expect(errors).toEqual([]);
  });
test("Escape during player expansion cannot reopen it late", async ({
  page,
}) => {
  await playLong(page);
  const before = await page.locator("#audio").evaluate((a) => a.currentTime);
  await page.locator("#open-player").click();
  await page.keyboard.press("Escape");
  await page.waitForFunction(
    () =>
      document.querySelector("#now-playing").dataset.transitioning !== "true",
  );
  await expect
    .poll(() => page.locator("#audio").evaluate((a) => a.currentTime))
    .toBeGreaterThan(before + 0.35);
  await expect(page.locator("#now-playing")).not.toBeVisible();
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
});
test("older-browser motion fallback retains the same native player", async ({
  page,
}) => {
  await page.addInitScript(() =>
    Object.defineProperty(HTMLElement.prototype, "showPopover", {
      value: undefined,
      configurable: true,
    }),
  );
  await playLong(page);
  const before = await page.locator("#audio").evaluate((a) => a.currentSrc);
  await page.locator("#open-player").click();
  await expect(page.locator("#now-playing")).toBeVisible();
  await page.locator("#close-player").click();
  await expect(page.locator("#now-playing")).not.toBeVisible();
  expect(await page.locator("#audio").evaluate((a) => a.currentSrc)).toBe(
    before,
  );
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
});

test("media pause preserves keyboard focus within an unchanged queue rail", async ({
  page,
}) => {
  await playLong(page);
  await page.locator("#open-player").press("Enter");
  const more = page.locator("#full-queue-items button").first();
  await more.press("Tab");
  await more.focus();
  await page.locator("#audio").evaluate((a) => a.pause());
  await expect(more).toBeFocused();
  await expect(page.locator("#audio")).toHaveJSProperty("paused", true);
});
test("catalog playback marker follows actual audio and survives navigation", async ({
  page,
}) => {
  await playLong(page);
  const row = () =>
    page
      .locator("#items li")
      .filter({ has: page.getByRole("button", { name: "long", exact: true }) });
  await expect(row().locator(".playback-mark")).toHaveText("Playing");
  await page.locator("#play-toggle").click();
  await expect(row().locator(".playback-mark")).toHaveText("Paused");
  await page.getByRole("button", { name: "Albums", exact: true }).click();
  await page.getByRole("button", { name: "Tracks", exact: true }).click();
  await expect(row().locator(".playback-mark")).toHaveText("Paused");
  await expect(row()).toHaveClass(/is-current/);
});
test("pending group art stays neutral until real metadata resolves", async ({
  page,
  request,
}) => {
  const albums = (await (await request.get("/api/v1/albums?limit=50")).json())
    .items;
  const album = albums.find((a) => a.display_title === "Browser Album");
  // Exercise optional-field fallback explicitly. Current catalog cards receive
  // presentation data directly and do not issue a metadata read per card.
  await page.route("**/api/v1/albums?*", async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    for (const item of data.items) {
      delete item.track_count;
      delete item.artwork_url;
      delete item.artist_credit;
    }
    await route.fulfill({ response, json: data });
  });
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  await page.route(
    `**/api/v1/albums/${album.id}/tracks?limit=12`,
    async (route) => {
      await gate;
      await route.continue();
    },
  );
  await page.goto("/");
  const card = page.getByRole("button", { name: "Browser Album", exact: true });
  await expect(card.locator("img")).toHaveAttribute("data-pending", "true");
  await expect(card.locator(".artwork-caption")).not.toBeVisible();
  expect(
    await card.locator("img").evaluate((i) => getComputedStyle(i).opacity),
  ).toBe("0");
  release();
  await expect(card.locator("img")).toHaveAttribute("data-pending", "false");
  await expect(card.locator(".cover-credit")).toHaveText("Browser Artist");
  await expect(card.locator(".artwork-caption")).toBeHidden();
});
test("empty favorites invites a working path into the real library", async ({
  page,
}) => {
  await page.route("**/api/v1/favorites?limit=4", (route) =>
    route.fulfill({ json: { items: [], next_cursor: null } }),
  );
  await page.goto("/");
  await page
    .getByRole("button", { name: "Find a favorite", exact: true })
    .click();
  await expect(page.locator("#view-title")).toHaveText("Tracks");
  await expect(
    page.getByRole("button", { name: "Browser Song", exact: true }),
  ).toBeVisible();
});

test("small-phone player keeps transport and save actions in the viewport", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await playLong(page);
  await page.locator("#open-player").click();
  await page.waitForFunction(
    () =>
      document.querySelector("#now-playing").dataset.transitioning !== "true",
  );
  for (const id of [
    "close-player",
    "full-play",
    "full-playlist",
    "full-queue",
    "full-seek",
  ]) {
    const box = await page.locator("#" + id).boundingBox();
    expect(box.y).toBeGreaterThanOrEqual(0);
    expect(box.y + box.height).toBeLessThanOrEqual(568);
    expect(box.height).toBeGreaterThanOrEqual(44);
  }
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
  ).toBe(false);
});

test("small-phone tagged track retains album access and visible controls", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "Browser Song", exact: true }).click();
  await page.locator("#open-player").click();
  await page.waitForFunction(
    () =>
      document.querySelector("#now-playing").dataset.transitioning !== "true",
  );
  await expect(page.locator("#full-album")).toHaveText("Browser Album");
  for (const id of [
    "close-player",
    "full-album",
    "full-play",
    "full-playlist",
    "full-queue",
    "full-seek",
  ]) {
    const box = await page.locator("#" + id).boundingBox();
    expect(box.y + box.height).toBeLessThanOrEqual(568);
    expect(box.height).toBeGreaterThanOrEqual(44);
  }
  await page.locator("#full-album").click();
  await expect(page.locator("#view-title")).toHaveText("Browser Album");
});
