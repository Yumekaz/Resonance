const { test, expect } = require("@playwright/test");
test.skip(!process.env.RESONANCE_M2_E2E, "Requires the real M2 journey server");
test.use({ serviceWorkers: "block" });

test("generated sleeves are stable across album, track and player; real art wins", async ({
  page,
}) => {
  await page.goto("/");
  const card = page.getByRole("button", { name: "Browser Album", exact: true });
  await expect(card.locator(".cover-credit")).toContainText("Browser Artist");
  await expect(card.locator(".cover-credit")).toContainText("2024");
  const sleeve = await card.locator("img").getAttribute("src");
  await expect(card.locator(".artwork-caption")).toHaveText("Library sleeve");
  const sources = await page
    .locator(".collection-grid img")
    .evaluateAll((imgs) => imgs.map((img) => img.getAttribute("src")));
  expect(new Set(sources).size).toBeGreaterThan(1);
  await card.click();
  await expect(page.locator(".detail-art")).toHaveAttribute("src", sleeve);
  await page.getByRole("button", { name: "Browser Song", exact: true }).click();
  await page.locator("#open-player").click();
  await expect(page.locator("#full-art")).toHaveAttribute("src", sleeve);
  await expect(page.locator("#full-ambient")).toHaveAttribute("src", sleeve);
  await expect(page.locator(".full-art-caption")).toBeHidden();
  // Exercise successful embedded-image delivery through the browser path.
  // The metadata transport is controlled here; no mock is used for playback.
  await page.keyboard.press("Escape");
  await page.route("**/api/v1/tracks?*", async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    data.items.find((t) => t.title === "long").artwork_url =
      "/assets/resonance-mark.png";
    await route.fulfill({ response, json: data });
  });
  await page.goto("/#tracks");
  const row = page
    .locator("#items li")
    .filter({ has: page.getByRole("button", { name: "long", exact: true }) });
  await expect(row.locator("img")).toHaveAttribute(
    "src",
    "/assets/resonance-mark.png",
  );
  await expect(row.locator("img")).toHaveAttribute("data-missing", "false");
});

test("failed embedded artwork falls back once without an image error loop", async ({
  page,
}) => {
  await page.route("**/api/v1/tracks?*", async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    data.items.find((t) => t.title === "long").artwork_url =
      "/assets/nonexistent-art.png";
    await route.fulfill({ response, json: data });
  });
  await page.goto("/#tracks");
  const row = page
    .locator("#items li")
    .filter({ has: page.getByRole("button", { name: "long", exact: true }) });
  await expect(row.locator("img")).toHaveAttribute("data-missing", "true");
  await expect
    .poll(() =>
      row
        .locator("img")
        .evaluate((img) => img.complete && img.naturalWidth > 0),
    )
    .toBe(true);
});

test("keyboard and reduced-motion dialog opening have no motion or autoplay", async ({
  page,
}) => {
  await page.goto("/");
  await page.locator("#open-player").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#now-playing")).toBeVisible();
  expect(
    await page
      .locator("#now-playing")
      .evaluate((el) => el.getAnimations().length),
  ).toBe(0);
  await page.keyboard.press("Escape");
  await expect(page.locator("#open-player")).toBeFocused();
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.locator("#open-player").click();
  await expect(page.locator("#now-playing")).toBeVisible();
  expect(
    await page
      .locator("#now-playing")
      .evaluate((el) => el.getAnimations().length),
  ).toBe(0);
  expect(await page.locator("#audio").evaluate((el) => el.paused)).toBe(true);
});

test("keyboard seeking moves the real audio position and keeps both timelines in sync", async ({
  page,
}) => {
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "long", exact: true }).click();
  await expect
    .poll(() =>
      page
        .locator("#audio")
        .evaluate((audio) => !audio.paused && audio.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#open-player").click();
  await page.locator("#full-play").click();
  await expect
    .poll(() => page.locator("#audio").evaluate((audio) => audio.paused))
    .toBe(true);
  const before = await page
    .locator("#audio")
    .evaluate((audio) => audio.currentTime);
  await page.locator("#full-seek").focus();
  for (let i = 0; i < 10; i++) await page.keyboard.press("ArrowRight");
  await expect
    .poll(() => page.locator("#audio").evaluate((audio) => audio.currentTime))
    .toBeGreaterThan(before + 2.5);
  await expect
    .poll(() =>
      page.evaluate(() => {
        const audio = document.querySelector("#audio");
        const value = Math.round((audio.currentTime / audio.duration) * 1000);
        return (
          audio.paused &&
          ["seek", "full-seek"].every(
            (id) =>
              Number(document.getElementById(id).value) === value &&
              document.getElementById(id + "-progress").value === value,
          )
        );
      }),
    )
    .toBe(true);
});

test("a pending menu action rejects repeat clicks and cannot close a newer menu", async ({
  page,
  request,
}) => {
  const before = await (await request.get("/api/v1/queue")).json();
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  let writes = 0;
  await page.route("**/api/v1/queue/items", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    writes++;
    await gate;
    await route.continue();
  });
  await page.goto("/#tracks");
  const row = (title) =>
    page
      .locator("#items li")
      .filter({ has: page.getByRole("button", { name: title, exact: true }) });
  await row("long")
    .getByRole("button", { name: /^More actions for / })
    .click();
  const action = page
    .locator(".context-menu")
    .getByRole("menuitem", { name: "Add to queue", exact: true });
  await action.dblclick();
  await expect.poll(() => writes).toBe(1);
  await expect(action).toBeDisabled();
  await expect(action).toHaveAttribute("aria-busy", "true");
  await page.keyboard.press("Escape");
  await row("Browser Song")
    .getByRole("button", { name: /^More actions for / })
    .click();
  await expect(page.locator(".menu-heading")).toHaveText("Browser Song");
  release();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).items.length,
    )
    .toBe(before.items.length + 1);
  await expect(page.locator(".context-menu")).toBeVisible();
  await expect(page.locator(".menu-heading")).toHaveText("Browser Song");
  expect(writes).toBe(1);
});

test("two recent tracks form a readable phone grid without a nested scrollbar", async ({
  page,
  request,
}) => {
  const tracks = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items;
  const recent = ["long", "Short"].map((title) =>
    tracks.find((t) => t.title === title),
  );
  expect(recent.every(Boolean)).toBe(true);
  await page.route("**/api/v1/history?*", (route) =>
    route.fulfill({
      json: {
        items: recent.map((t) => ({
          track_id: t.id,
          title: t.title,
          artist_credit: t.artist_credit,
          available: t.available,
        })),
        next_cursor: null,
      },
    }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const shelf = page.locator(".recent-section");
  await expect(shelf).toHaveClass(/paired-shelf/);
  await expect(shelf.locator("li")).toHaveCount(2);
  expect(
    await shelf
      .locator(".collection-grid")
      .evaluate((el) => el.scrollWidth <= el.clientWidth),
  ).toBe(true);
  const boxes = await shelf.locator(".artwork-frame").evaluateAll((els) =>
    els.map((el) => ({
      width: el.getBoundingClientRect().width,
      x: el.getBoundingClientRect().x,
    })),
  );
  expect(boxes.every((box) => box.width >= 150)).toBe(true);
  expect(boxes[1].x).toBeGreaterThan(boxes[0].x + 150);
});
