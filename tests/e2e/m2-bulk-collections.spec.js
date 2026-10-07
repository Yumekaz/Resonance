const { test, expect } = require("@playwright/test");
test.skip(
  !process.env.RESONANCE_M2_COLLECTION_E2E,
  "Separate collection preview required",
);
test.use({ serviceWorkers: "block" });
async function write(request, path, data) {
  const response = await request.post(path, {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data,
  });
  expect(response.ok(), await response.text()).toBe(true);
  return response.json();
}
async function playlist(request, name) {
  return write(request, "/api/v1/playlists", { name, expected_version: 0 });
}
async function choose(page, name) {
  await page.getByRole("button", { name: /^Choose playlist:/ }).click();
  await page.getByRole("menuitemradio", { name, exact: true }).click();
  await page.getByRole("button", { name: "Add track", exact: true }).click();
  await expect(page.locator("#action-dialog")).toBeHidden();
}
test("queue multi-select preserves playing audio and current token through group move/removal", async ({
  page,
  request,
}) => {
  const tracks = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items;
  const long = (
    await (await request.get("/api/v1/search?q=long&limit=50")).json()
  ).tracks.find((t) => t.title === "long");
  const q0 = await (await request.get("/api/v1/queue")).json();
  await write(request, "/api/v1/queue/collection", {
    track_ids: [long.id, tracks[0].id, long.id, tracks[1].id],
    placement: "replace",
    expected_version: q0.revision,
  });
  await page.goto("/#queue");
  await page.locator("#play-toggle").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  const before = await (await request.get("/api/v1/queue")).json();
  await page.locator("#select-songs").click();
  await page.locator("#items .selection-check input").nth(2).check();
  await page.locator("#items .selection-check input").nth(3).check();
  await expect(page.locator("#selection-count")).toHaveText("2 selected");
  await page.locator("#selected-next").click();
  await expect
    .poll(async () => {
      const q = await (await request.get("/api/v1/queue")).json();
      return q.items[1].id;
    })
    .toBe(before.items[2].id);
  const moved = await (await request.get("/api/v1/queue")).json();
  expect(moved.current_item_id).toBe(before.current_item_id);
  expect(moved.selection_token).toBe(before.selection_token);
  await expect(page.locator("#selection-count")).toHaveText("2 selected");
  await page.locator("#selected-remove").click();
  await page
    .locator("#action-dialog")
    .getByRole("button", { name: "Remove selected", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).items.length,
    )
    .toBe(2);
  expect(await page.locator("#audio").evaluate((a) => !a.paused)).toBe(true);
});
test("playlist groups copy and move duplicate occurrences through one destination picker", async ({
  page,
  request,
}) => {
  const track = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items[0];
  const sourceName = "Bulk source " + crypto.randomUUID().slice(0, 8),
    targetName = "Bulk target " + crypto.randomUUID().slice(0, 8);
  const source = await playlist(request, sourceName),
    target = await playlist(request, targetName);
  for (let n = 0; n < 3; n++)
    await write(request, `/api/v1/playlists/${source.id}/items`, {
      track_id: track.id,
      expected_version: n,
    });
  try {
    await page.goto("/#playlists");
    await page.getByRole("button", { name: sourceName, exact: true }).click();
    await page.locator("#select-songs").click();
    await page.locator("#items .selection-check input").nth(0).check();
    await page.locator("#items .selection-check input").nth(2).check();
    await page.locator("#selected-save").click();
    await choose(page, targetName);
    let original = await (
      await request.get(`/api/v1/playlists/${source.id}`)
    ).json();
    let copied = await (
      await request.get(`/api/v1/playlists/${target.id}`)
    ).json();
    expect(original.items.length).toBe(3);
    expect(copied.items.length).toBe(2);
    expect(copied.items[0].id).not.toBe(copied.items[1].id);
    await expect(page.locator("#selection-count")).toHaveText("2 selected");
    await page.route("**/api/v1/queue", (route) => route.abort());
    await page.locator("#selected-queue-next").click();
    await expect(page.locator("#status")).toContainText(
      "Up Next is unavailable",
    );
    await expect(page.locator("#selection-count")).toHaveText("2 selected");
    await page.unroute("**/api/v1/queue");
    await page.locator("#selected-move-save").click();
    await choose(page, targetName);
    original = await (
      await request.get(`/api/v1/playlists/${source.id}`)
    ).json();
    copied = await (await request.get(`/api/v1/playlists/${target.id}`)).json();
    expect(original.items.length).toBe(1);
    expect(copied.items.length).toBe(4);
  } finally {
    for (const p of [source, target]) {
      const current = await (
        await request.get(`/api/v1/playlists/${p.id}`)
      ).json();
      await request.delete(`/api/v1/playlists/${p.id}`, {
        data: { expected_version: current.revision },
      });
    }
  }
});
