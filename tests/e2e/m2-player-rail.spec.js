const { test, expect } = require("@playwright/test");
const { trackAction } = require("./m2-ui");
test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Run against the real M2 journey server",
);
test.use({ serviceWorkers: "block" });
test.setTimeout(60000);

async function clearQueue(request) {
  const q = await (await request.get("/api/v1/queue")).json();
  const response = await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  expect(response.ok()).toBe(true);
}
async function add(request, trackID, placement) {
  const q = await (await request.get("/api/v1/queue")).json();
  const response = await request.post("/api/v1/queue/items", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { track_id: trackID, placement, expected_version: q.revision },
  });
  expect(response.ok()).toBe(true);
  return response.json();
}

test("dark player queue rail preserves duplicate occurrences and edits real queue state", async ({
  page,
  request,
}) => {
  await clearQueue(request);
  const tracks = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items;
  const long = tracks.find((t) => t.title === "long");
  const browser = tracks.find((t) => t.title === "Browser Song");
  await add(request, long.id, "end");
  await add(request, browser.id, "end");
  await page.goto("/#tracks");
  await trackAction(
    page,
    page.locator("#items li").filter({ hasText: "long" }),
    "Play now",
  );
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#open-player").click();
  await expect(page.locator("#full-queue-items li")).toHaveCount(3);
  await expect(
    page.locator("#full-queue-items li").filter({ hasText: "long" }),
  ).toHaveCount(2);
  const before = await (await request.get("/api/v1/queue")).json();
  const duplicate = page
    .locator("#full-queue-items li")
    .filter({ hasText: "long" })
    .nth(1);
  await duplicate.getByRole("button", { name: /^More actions for / }).click();
  await page
    .locator(".context-menu")
    .getByRole("menuitem", { name: "Move down", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).items[2].id,
    )
    .toBe(before.items[1].id);
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.current_item_id).toBe(before.current_item_id);
  expect(after.selection_token).toBe(before.selection_token);
  const row = page
    .locator("#full-queue-items li")
    .filter({ hasText: "Browser Song" });
  await row.getByRole("button", { name: /^More actions for / }).click();
  await page
    .locator(".context-menu")
    .getByRole("menuitem", { name: "Remove", exact: true })
    .click();
  await expect(page.locator("#full-queue-items li")).toHaveCount(2);
  expect(await page.locator("#audio").evaluate((a) => !a.paused)).toBe(true);
  await clearQueue(request);
});

test("refreshing the rail cannot give stale browser playback a newer selection token", async ({
  page,
  request,
}) => {
  await clearQueue(request);
  const long = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.find((t) => t.title === "long");
  await page.goto("/#tracks");
  await trackAction(
    page,
    page.locator("#items li").filter({ hasText: "long" }),
    "Play now",
  );
  await expect
    .poll(() => page.locator("#audio").evaluate((a) => a.currentTime > 0.1))
    .toBe(true);
  await page.locator("#open-player").click();
  await expect(page.locator("#full-queue-items li")).toHaveCount(1);
  await add(request, long.id, "now");
  const newer = await (await request.get("/api/v1/queue")).json();
  await page.locator("#full-queue-refresh").click();
  await expect(page.locator("#full-queue-status")).toContainText(
    "queue changed",
  );
  await expect(page.locator("#full-next")).toBeDisabled();
  await page.keyboard.press("Escape");
  await page.locator("#open-queue").click();
  const response = page.waitForResponse(
    (r) =>
      r.url().endsWith("/api/v1/queue/advance") &&
      r.request().method() === "POST",
  );
  await page.locator("#next").click();
  expect((await response).status()).toBe(409);
  await expect(page.locator("#status")).toContainText(
    "Queue selection changed",
  );
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.revision).toBe(newer.revision);
  expect(after.current_item_id).toBe(newer.current_item_id);
  expect(after.selection_token).toBe(newer.selection_token);
  await clearQueue(request);
});

test("saved selection is ready after reload and resumes without inserting another occurrence", async ({
  page,
  request,
}) => {
  await clearQueue(request);
  const long = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.find((t) => t.title === "long");
  await add(request, long.id, "now");
  const before = await (await request.get("/api/v1/queue")).json();
  await page.goto("/");
  await expect(page.locator("#now-title")).toHaveText("long");
  await expect(page.locator("#play-toggle")).toBeEnabled();
  expect(await page.locator("#audio").getAttribute("src")).toBeNull();
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(true);
  await page.locator("#play-toggle").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.items.map((i) => i.id)).toEqual(before.items.map((i) => i.id));
  expect(after.revision).toBe(before.revision);
  expect(after.selection_token).toBe(before.selection_token);
  await page.reload();
  await expect(page.locator("#now-title")).toHaveText("long");
  expect(await page.locator("#audio").getAttribute("src")).toBeNull();
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(true);
  await clearQueue(request);
});
