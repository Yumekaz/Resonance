const { test, expect } = require("@playwright/test");
const crypto = require("node:crypto");
test.skip(!process.env.RESONANCE_M2_E2E, "Requires real disposable M2 server");
test.use({ serviceWorkers: "block" });
async function seed(request, names) {
  let q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  q = await (await request.get("/api/v1/queue")).json();
  const tracks = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items;
  const response = await request.post("/api/v1/queue/collection", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      track_ids: names.map((name) => tracks.find((t) => t.title === name).id),
      placement: "now",
      expected_version: q.revision,
    },
  });
  expect(response.ok()).toBe(true);
  return await (await request.get("/api/v1/queue")).json();
}
async function openPlayer(page) {
  await page.goto("/#queue");
  await page.locator("#open-player").click();
  await expect(page.locator("#now-playing")).toBeVisible();
}
test("shuffle reorders occurrences, preserves audio/current authority and restores upcoming order", async ({
  page,
  request,
}) => {
  const q = await seed(request, [
    "long",
    "Browser Song",
    "Short",
    "Browser Song",
  ]);
  await openPlayer(page);
  await page.locator("#full-play").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#full-shuffle").click();
  await expect(page.locator("#full-shuffle")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  let after = await (await request.get("/api/v1/queue")).json();
  expect(after.current_item_id).toBe(q.current_item_id);
  expect(after.selection_token).toBe(q.selection_token);
  expect(after.items.map((i) => i.id).sort()).toEqual(
    q.items.map((i) => i.id).sort(),
  );
  expect(after.items.map((i) => i.id)).not.toEqual(q.items.map((i) => i.id));
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
  await page.locator("#full-shuffle").click();
  await expect(page.locator("#full-shuffle")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  after = await (await request.get("/api/v1/queue")).json();
  expect(after.items.map((i) => i.id)).toEqual(q.items.map((i) => i.id));
  await expect(page.locator("#full-shuffle")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
});
test("repeat one naturally replays a real song with fresh authority; Next deliberately skips", async ({
  page,
  request,
}) => {
  test.setTimeout(30000);
  await seed(request, ["Short", "long"]);
  await openPlayer(page);
  await page.locator("#full-repeat").click();
  await page.locator("#full-repeat").click();
  await expect(page.locator("#full-repeat")).toHaveAttribute(
    "aria-label",
    "Repeat one",
  );
  await page.locator("#full-play").click();
  const before = await (await request.get("/api/v1/queue")).json();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).selection_token,
      { timeout: 15000 },
    )
    .not.toBe(before.selection_token);
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.current_item_id).toBe(before.current_item_id);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#full-next").click();
  await expect(page.locator("#full-title")).toHaveText("long");
});
test("repeat all wraps natural queue ending; preferences survive reload without autoplay", async ({
  page,
  request,
}) => {
  test.setTimeout(30000);
  const q = await seed(request, ["Short", "Short"]);
  await openPlayer(page);
  await page.locator("#full-repeat").click();
  await expect(page.locator("#full-repeat")).toHaveAttribute(
    "aria-label",
    "Repeat all",
  );
  await page.locator("#full-play").click();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).current_item_id,
      { timeout: 15000 },
    )
    .toBe(q.items[1].id);
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).current_item_id,
      { timeout: 15000 },
    )
    .toBe(q.items[0].id);
  await page.reload();
  await page.locator("#open-player").click();
  await expect(page.locator("#full-repeat")).toHaveAttribute(
    "aria-label",
    "Repeat all",
  );
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(true);
  expect(await page.locator("#audio").getAttribute("src")).toBeNull();
});
for (const width of [320, 390, 430, 844])
  test(`playback modes remain reachable at ${width}px`, async ({
    page,
    request,
  }) => {
    await seed(request, ["long", "Short"]);
    await page.setViewportSize({
      width,
      height: width === 844 ? 390 : width === 320 ? 568 : 844,
    });
    await openPlayer(page);
    for (const id of [
      "full-shuffle",
      "full-repeat",
      "full-play",
      "full-next",
      "full-previous",
    ]) {
      const box = await page.locator("#" + id).boundingBox();
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(width);
      expect(box.y + box.height).toBeLessThanOrEqual(
        width === 844 ? 390 : width === 320 ? 568 : 844,
      );
      expect(box.height).toBeGreaterThanOrEqual(44);
    }
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.locator("#full-repeat").press("Enter");
    await expect(page.locator("#full-repeat")).toHaveAttribute(
      "aria-label",
      "Repeat all",
    );
  });

test("repeat off stops naturally at the end; solo repeat does not advance the saved queue", async ({
  page,
  request,
}) => {
  test.setTimeout(30000);
  let q = await seed(request, ["Short"]);
  await openPlayer(page);
  await page.locator("#full-play").click();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).selection_state,
      { timeout: 15000 },
    )
    .toBe("stopped");
  expect(
    await page.locator("#audio").evaluate((a) => a.paused && a.ended),
  ).toBe(true);
  await page.locator("#close-player").click();
  await page.goto("/#tracks");
  const row = page
    .locator("#items .track-row")
    .filter({ has: page.getByRole("button", { name: "Short", exact: true }) });
  await row.getByRole("button", { name: /More actions/ }).click();
  await page
    .locator(".context-menu")
    .getByRole("menuitem", { name: "Play this song only", exact: true })
    .click();
  await page.locator("#open-player").click();
  await page.locator("#full-repeat").click();
  await page.locator("#full-repeat").click();
  q = await (await request.get("/api/v1/queue")).json();
  let endedEvents = 0;
  page.on("response", (r) => {
    if (
      r.url().includes("/report") &&
      r.request().postData()?.includes("ended")
    )
      endedEvents++;
  });
  await expect.poll(() => endedEvents, { timeout: 15000 }).toBeGreaterThan(0);
  await expect
    .poll(() =>
      page
        .locator("#audio")
        .evaluate((a) => !a.paused && a.currentTime > 0.1 && !a.ended),
    )
    .toBe(true);
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.revision).toBe(q.revision);
  expect(after.selection_token).toBe(q.selection_token);
});
