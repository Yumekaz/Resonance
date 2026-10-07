const { test, expect } = require("@playwright/test");
test.skip(
  !process.env.RESONANCE_M2_COLLECTION_E2E,
  "Separate collection fixture preview required",
);
test.use({ serviceWorkers: "block" });
test.setTimeout(60000);
async function start(request, track) {
  const q = await (await request.get("/api/v1/queue")).json();
  const result = await request.post("/api/v1/queue/context", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      source: { kind: "library", order: "title" },
      start_track_id: track.id,
      shuffle: false,
      expected_version: q.revision,
    },
  });
  expect(result.ok()).toBe(true);
  return await (await request.get("/api/v1/queue")).json();
}
test("Library play at a visible page edge continues to the next page with real audio", async ({
  page,
  request,
}) => {
  const first = await (await request.get("/api/v1/tracks?limit=50")).json();
  const second = await (
    await request.get(
      "/api/v1/tracks?limit=50&cursor=" + encodeURIComponent(first.next_cursor),
    )
  ).json();
  const edge = first.items.at(-1),
    next = second.items[0];
  await page.goto("/#tracks");
  await page.getByRole("button", { name: edge.title, exact: true }).click();
  await expect(page.locator("#now-title")).toHaveText(edge.title);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  const q = await (await request.get("/api/v1/queue")).json();
  expect(q.context.total).toBeGreaterThan(50);
  expect(q.items.length).toBeLessThanOrEqual(128);
  await page.locator("#player-next").click();
  await expect(page.locator("#now-title")).toHaveText(next.title);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
});
test("natural ending at a materialized window edge refills and advances atomically", async ({
  page,
  request,
}) => {
  const tracks = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items;
  const first = tracks.find((t) => t.title === "Collection Song 0000");
  let q = await start(request, first);
  const last = q.items.at(-1);
  const selected = await request.post("/api/v1/queue/select", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { item_id: last.id, expected_version: q.revision },
  });
  expect(selected.ok()).toBe(true);
  q = await (await request.get("/api/v1/queue")).json();
  expect(q.context.more).toBe(true);
  await page.goto("/#queue");
  await page.locator("#play-toggle").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await expect
    .poll(
      async () => {
        const now = await (await request.get("/api/v1/queue")).json();
        return (
          now.selection_state === "selected" && now.current_item_id !== last.id
        );
      },
      { timeout: 15000 },
    )
    .toBe(true);
  const after = await (await request.get("/api/v1/queue")).json();
  expect(after.items.length).toBeLessThanOrEqual(229);
  expect(after.context.id).toBe(q.context.id);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.05),
    )
    .toBe(true);
});
test("source boundary rail does not present the cropped window as the next complete round", async ({
  page,
  request,
}) => {
  const long = (
    await (await request.get("/api/v1/search?q=long&limit=50")).json()
  ).tracks.find((t) => t.title === "long");
  const first = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.find((t) => t.title === "Collection Song 0000");
  const q = await (await request.get("/api/v1/queue")).json();
  const started = await request.post("/api/v1/queue/context", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      source: {
        kind: "selection",
        order: "original",
        track_ids: [first.id, long.id],
      },
      start_track_id: long.id,
      expected_version: q.revision,
    },
  });
  expect(started.ok()).toBe(true);
  await page.goto("/#queue");
  await page.locator("#play-toggle").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#open-player").click();
  await expect(page.locator("#full-queue-status")).toContainText(
    "A new round from Selected songs",
  );
  await expect(
    page.locator("#full-queue-items .queue-selection", {
      hasText: "Next round",
    }),
  ).toHaveCount(0);
});
test("Favorites playback continues beyond its visible page", async ({
  page,
  request,
}) => {
  let cursor = null,
    tracks = [];
  do {
    const data = await (
      await request.get(
        "/api/v1/tracks?limit=50" +
          (cursor ? "&cursor=" + encodeURIComponent(cursor) : ""),
      )
    ).json();
    tracks.push(...data.items.filter((t) => t.available));
    cursor = data.next_cursor;
  } while (tracks.length < 60 && cursor);
  tracks = tracks.slice(0, 60);
  expect(tracks.length).toBe(60);
  const owned = [];
  try {
    for (const track of tracks) {
      const added = await request.put(`/api/v1/favorites/tracks/${track.id}`);
      expect(added.ok()).toBe(true);
      owned.push(track.id);
    }
    const data = await (await request.get("/api/v1/favorites?limit=50")).json();
    await page.goto("/#favorites");
    await expect(page.locator("#items .track-row")).toHaveCount(50);
    await page
      .getByRole("button", { name: data.items[0].title, exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await (await request.get("/api/v1/queue")).json()).context?.kind,
      )
      .toBe("favorites");
    const queue = await (await request.get("/api/v1/queue")).json();
    expect(queue.context.total).toBeGreaterThanOrEqual(60);
    expect(queue.items.length).toBeGreaterThan(50);
  } finally {
    for (const id of owned)
      await request.delete(`/api/v1/favorites/tracks/${id}`);
  }
});
