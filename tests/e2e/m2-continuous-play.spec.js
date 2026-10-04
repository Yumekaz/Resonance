const { test, expect } = require("@playwright/test");
const crypto = require("node:crypto");
test.skip(!process.env.RESONANCE_M2_E2E, "Requires real M2 catalog");
test.use({ serviceWorkers: "block" });
async function seed(request, names, start = 0) {
  let q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  q = await (await request.get("/api/v1/queue")).json();
  const ts = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items;
  const r = await request.post("/api/v1/queue/collection", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      track_ids: names.map((n) => ts.find((t) => t.title === n).id),
      placement: "replace",
      start_index: start,
      expected_version: q.revision,
    },
  });
  expect(r.ok()).toBe(true);
  return await (await request.get("/api/v1/queue")).json();
}
async function open(page) {
  await page.goto("/#queue");
  await page.locator("#open-player").click();
  await expect(page.locator("#full-keep-playing")).toBeChecked();
}
test("default continuous playback survives multiple real endings without queue growth", async ({
  page,
  request,
}) => {
  test.setTimeout(30000);
  const q = await seed(request, ["Short", "Short"]);
  const transitions = [];
  page.on("response", async (r) => {
    if (r.url().endsWith("/api/v1/queue/advance") && r.ok())
      transitions.push(await r.json());
  });
  await open(page);
  await expect(page.locator("#full-repeat")).toHaveAttribute(
    "aria-label",
    "Repeat off",
  );
  await page.locator("#full-play").click();
  await expect
    .poll(() => transitions.length, { timeout: 18000 })
    .toBeGreaterThanOrEqual(3);
  expect(transitions.slice(0, 3).map((c) => c.current_item_id)).toEqual([
    q.items[1].id,
    q.items[0].id,
    q.items[1].id,
  ]);
  expect(new Set(transitions.map((c) => c.selection_token)).size).toBe(
    transitions.length,
  );
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.items).toHaveLength(2);
  expect(after.selection_state).toBe("selected");
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.reload();
  await page.locator("#open-player").click();
  await expect(page.locator("#full-keep-playing")).toBeChecked();
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(true);
});
test("a shuffled natural boundary starts a new actual order and avoids the song just ended", async ({
  page,
  request,
}) => {
  test.setTimeout(30000);
  const q = await seed(request, ["long", "Browser Song", "Short"], 2);
  await open(page);
  await page.locator("#full-shuffle").click();
  await expect(page.locator("#full-shuffle")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await page.locator("#full-play").click();
  await expect(page.locator("#full-queue-status")).toContainText(
    "fresh shuffled round",
  );
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).selection_token,
      { timeout: 15000 },
    )
    .not.toBe(q.selection_token);
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.items.map((i) => i.id).sort()).toEqual(
    q.items.map((i) => i.id).sort(),
  );
  expect(after.items.map((i) => i.id)).not.toEqual(q.items.map((i) => i.id));
  expect(after.current_item_id).toBe(after.items[0].id);
  expect(after.items[0].track_id).not.toBe(q.items[2].track_id);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
});
test("turning continuous playback off stops naturally and persists across reload", async ({
  page,
  request,
}) => {
  test.setTimeout(30000);
  await seed(request, ["Short"]);
  await open(page);
  await page.locator("#full-keep-playing").uncheck();
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
  await page.reload();
  await page.locator("#open-player").click();
  await expect(page.locator("#full-keep-playing")).not.toBeChecked();
});
test("the next round is visible and keyboard-toggleable at the final ordered song", async ({
  page,
  request,
}) => {
  await seed(request, ["long", "Browser Song"], 1);
  await open(page);
  await expect(page.locator("#full-next")).toBeEnabled();
  await expect(page.locator("#full-queue-items")).toContainText("Next round");
  await page.locator("#full-keep-playing").press("Space");
  await expect(page.locator("#full-keep-playing")).not.toBeChecked();
  await expect(page.locator("#full-next")).toBeDisabled();
  await page.locator("#full-keep-playing").press("Space");
  await expect(page.locator("#full-next")).toBeEnabled();
});

test("an acknowledged handoff shows Changing song while next metadata is pending", async ({
  page,
  request,
}) => {
  const q = await seed(request, ["long", "Browser Song"]);
  await open(page);
  await page.locator("#full-play").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  let release, seen;
  const gate = new Promise((r) => (release = r)),
    started = new Promise((r) => (seen = r));
  await page.route("**/api/v1/tracks/" + q.items[1].track_id, async (route) => {
    seen();
    await gate;
    await route.continue();
  });
  try {
    await page.locator("#full-next").click();
    await started;
    await expect(page.locator("#listening-context")).toHaveText(
      "Changing song…",
    );
    await expect(page.locator("#full-queue-status")).toHaveText(
      "Changing song…",
    );
    await expect(page.locator("#full-seek")).toBeDisabled();
    release();
    await expect(page.locator("#full-title")).toHaveText("Browser Song");
    await expect
      .poll(() =>
        page
          .locator("#audio")
          .evaluate((a) => !a.paused && a.currentTime > 0.1),
      )
      .toBe(true);
  } finally {
    release();
  }
});
