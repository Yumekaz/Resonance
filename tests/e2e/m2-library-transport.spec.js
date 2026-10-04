const { test, expect } = require("@playwright/test");
const crypto = require("node:crypto");
test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Requires the real disposable M2 catalog",
);
test.use({ serviceWorkers: "block" });
async function clear(request) {
  const q = await (await request.get("/api/v1/queue")).json();
  expect(
    (
      await request.delete("/api/v1/queue", {
        headers: { "Idempotency-Key": crypto.randomUUID() },
        data: { expected_version: q.revision },
      })
    ).ok(),
  ).toBe(true);
}
test("Library song click queues the visible context; Next and Previous continue playing", async ({
  page,
  request,
}) => {
  await clear(request);
  const tracks = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.filter((t) => t.available);
  const index = tracks.findIndex((t) => t.title === "Guest A");
  expect(index).toBeGreaterThan(0);
  expect(index).toBeLessThan(tracks.length - 1);
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "Guest A", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).items.length,
    )
    .toBe(tracks.length);
  let q = await (await request.get("/api/v1/queue")).json();
  expect(q.items.map((i) => i.track_id)).toEqual(tracks.map((t) => t.id));
  expect(q.current_item_id).toBe(q.items[index].id);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#open-player").click();
  await page.locator("#full-next").click();
  await expect(page.locator("#full-title")).toHaveText(tracks[index + 1].title);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#full-previous").click();
  await expect(page.locator("#full-title")).toHaveText("Guest A");
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await expect(page.locator("#listening-context")).not.toContainText(
    "queue changed",
  );
});
test("Next at a one-song queue boundary cannot stop a playing song", async ({
  page,
  request,
}) => {
  await clear(request);
  const track = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.find((t) => t.title === "long");
  let q = await (await request.get("/api/v1/queue")).json();
  await request.post("/api/v1/queue/items", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      track_id: track.id,
      placement: "now",
      expected_version: q.revision,
    },
  });
  await page.goto("/#queue");
  await page.locator("#open-player").click();
  await page.locator("#full-keep-playing").uncheck();
  await page.locator("#full-play").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await expect(page.locator("#full-next")).toBeDisabled();
  q = await (await request.get("/api/v1/queue")).json();
  // Media Session and programmatic input must respect the same boundary guard.
  await page.locator("#next").dispatchEvent("click");
  expect((await (await request.get("/api/v1/queue")).json()).revision).toBe(
    q.revision,
  );
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
});

test("shuffle Library playback keeps the chosen song and restores upcoming order when turned off", async ({
  page,
  request,
}) => {
  await clear(request);
  const tracks = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.filter((t) => t.available);
  await page.goto("/#tracks");
  await page.locator("#player-shuffle").click();
  await expect(page.locator("#player-shuffle")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await page.getByRole("button", { name: "Guest A", exact: true }).click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  let q = await (await request.get("/api/v1/queue")).json();
  const selected = q.items.find((i) => i.id === q.current_item_id);
  expect(selected.track_id).toBe(tracks.find((t) => t.title === "Guest A").id);
  expect(q.items).toHaveLength(tracks.length);
  const before = { id: q.current_item_id, token: q.selection_token };
  await page.locator("#player-shuffle").click();
  await expect(page.locator("#player-shuffle")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  q = await (await request.get("/api/v1/queue")).json();
  expect(q.current_item_id).toBe(before.id);
  expect(q.selection_token).toBe(before.token);
  expect(q.items.slice(1).map((i) => i.track_id)).toEqual(
    tracks.filter((t) => t.id !== selected.track_id).map((t) => t.id),
  );
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
});
