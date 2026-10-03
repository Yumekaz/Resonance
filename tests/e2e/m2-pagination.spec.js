const { test, expect } = require("@playwright/test");
const { trackAction } = require("./m2-ui");
test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Run against the real M2 journey server",
);
test.use({ serviceWorkers: "block" });
test.setTimeout(90000);
test("queue and playlist pages stay bounded and reorder the correct duplicate occurrence", async ({
  page,
  request,
}) => {
  const track = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.find((t) => t.title === "Browser Song");
  let q = await (await request.get("/api/v1/queue")).json();
  let response = await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  expect(response.ok()).toBe(true);
  q = await response.json();
  const name = "M2 pagination " + crypto.randomUUID().slice(0, 8);
  response = await request.post("/api/v1/playlists", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { name, expected_version: 0 },
  });
  expect(response.ok()).toBe(true);
  let playlist = await response.json();
  try {
    await page.route("**/user-library.js", async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 200));
      await route.continue();
    });
    for (let i = 0; i < 101; i++) {
      response = await request.post("/api/v1/queue/items", {
        headers: { "Idempotency-Key": crypto.randomUUID() },
        data: {
          track_id: track.id,
          placement: "end",
          expected_version: q.revision,
        },
      });
      expect(response.ok()).toBe(true);
      q = await response.json();
      response = await request.post(`/api/v1/playlists/${playlist.id}/items`, {
        headers: { "Idempotency-Key": crypto.randomUUID() },
        data: { track_id: track.id, expected_version: playlist.revision },
      });
      expect(response.ok()).toBe(true);
      const change = await response.json();
      playlist.revision = change.revision;
    }
    const before = await (await request.get("/api/v1/queue")).json();
    await page.goto("/#queue");
    await expect(page.locator("#items .track-row")).toHaveCount(100);
    await page.locator("#more").click();
    await expect(page.locator("#items .track-row")).toHaveCount(1);
    await trackAction(page, page.locator("#items .track-row"), "Move up");
    await expect
      .poll(
        async () =>
          (await (await request.get("/api/v1/queue")).json()).items[99].id,
      )
      .toBe(before.items[100].id);
    await expect(page.locator("#items .track-row")).toHaveCount(100);
    await expect(page.locator("#previous-page")).toBeHidden();
    await expect(
      page.locator(`#items [data-item-id="${before.items[100].id}"]`),
    ).toBeVisible();
    await page.getByRole("button", { name: "Playlists", exact: true }).click();
    await page.getByRole("button", { name, exact: true }).click();
    await expect(page.locator("#items .track-row")).toHaveCount(100);
    await page.locator("#more").click();
    await expect(page.locator("#items .track-row")).toHaveCount(1);
    const beforePlaylist = await (
      await request.get(`/api/v1/playlists/${playlist.id}`)
    ).json();
    await trackAction(page, page.locator("#items .track-row"), "Move up");
    await expect
      .poll(
        async () =>
          (await (await request.get(`/api/v1/playlists/${playlist.id}`)).json())
            .items[99].id,
      )
      .toBe(beforePlaylist.items[100].id);
    await expect(page.locator("#items .track-row")).toHaveCount(100);
    await expect(page.locator("#previous-page")).toBeHidden();
    await expect(
      page.locator(`#items [data-item-id="${beforePlaylist.items[100].id}"]`),
    ).toBeVisible();
  } finally {
    const detail = await (
      await request.get(`/api/v1/playlists/${playlist.id}`)
    ).json();
    await request.delete(`/api/v1/playlists/${playlist.id}`, {
      data: { expected_version: detail.revision },
    });
    q = await (await request.get("/api/v1/queue")).json();
    await request.delete("/api/v1/queue", {
      headers: { "Idempotency-Key": crypto.randomUUID() },
      data: { expected_version: q.revision },
    });
  }
});
