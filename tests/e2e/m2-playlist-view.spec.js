const { test, expect } = require("@playwright/test");
const { AxeBuilder } = require("@axe-core/playwright");
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
async function choose(page, selector, label) {
  await page.locator(selector).click();
  await page.getByRole("menuitemradio", { name: label, exact: true }).click();
}
test("playlist search covers every page; sorts are views and playback follows the chosen view", async ({
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
    tracks.push(...data.items);
    cursor = data.next_cursor;
  } while (tracks.length < 160 && cursor);
  tracks = tracks.slice(0, 160);
  const name = "View check " + crypto.randomUUID().slice(0, 8);
  const created = await write(request, "/api/v1/playlists", {
    name,
    expected_version: 0,
  });
  const initial = await (await request.get("/api/v1/queue")).json();
  await write(request, "/api/v1/queue/collection", {
    track_ids: tracks.map((t) => t.id),
    placement: "replace",
    expected_version: initial.revision,
  });
  const q = await (await request.get("/api/v1/queue")).json();
  await write(request, "/api/v1/collections/edit", {
    source: { kind: "queue" },
    item_ids: q.items.map((item) => item.id),
    action: "copy",
    expected_version: q.revision,
    target: { kind: "playlist", id: created.id },
    target_version: 0,
    placement: "",
  });
  try {
    await page.goto("/#playlists");
    await page.getByRole("button", { name, exact: true }).click();
    await expect(page.locator("#items .track-row")).toHaveCount(100);
    await page
      .getByRole("searchbox", { name: "Search this playlist", exact: true })
      .fill(tracks.at(-1).title);
    await expect(page.locator("#items .track-row")).toHaveCount(1);
    await expect(page.locator("#playlist-view-hint")).toContainText("1 of 160");
    await page.locator("#playlist-query-clear").click();
    await expect(page.locator("#items .track-row")).toHaveCount(100);
    const before = await (
      await request.get(`/api/v1/playlists/${created.id}`)
    ).json();
    await choose(page, "#playlist-order-trigger", "Title Z–A");
    const firstVisible = await page
      .locator("#items .track-row .item-title")
      .first()
      .textContent();
    const after = await (
      await request.get(`/api/v1/playlists/${created.id}`)
    ).json();
    expect(after.revision).toBe(before.revision);
    expect(after.items.map((item) => item.id)).toEqual(
      before.items.map((item) => item.id),
    );
    await page
      .getByRole("button", { name: "Play playlist", exact: true })
      .click();
    await expect(page.locator("#now-title")).toHaveText(firstVisible);
    await expect
      .poll(
        async () =>
          (await (await request.get("/api/v1/queue")).json()).context?.kind,
      )
      .toBe("playlist");
    const playing = await (await request.get("/api/v1/queue")).json();
    expect(playing.context.kind).toBe("playlist");
    expect(playing.context.total).toBe(160);
    await page.locator("#play-toggle").click();
    for (const size of [
      { width: 320, height: 568 },
      { width: 390, height: 844 },
      { width: 430, height: 932 },
      { width: 844, height: 390 },
      { width: 768, height: 1024 },
      { width: 1440, height: 900 },
    ]) {
      await page.setViewportSize(size);
      await expect(page.locator("#playlist-query")).toBeVisible();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await page.locator("#playlist-order-trigger").press("Enter");
      await expect(
        page.getByRole("menuitemradio", { name: "Title Z–A", exact: true }),
      ).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.locator("#playlist-order-trigger")).toBeFocused();
    }
    const axe = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
      .analyze();
    expect(axe.violations).toEqual([]);
    await choose(page, "#playlist-density-trigger", "Compact");
    await expect(page.locator("body")).toHaveAttribute(
      "data-density",
      "compact",
    );
    await page
      .getByRole("searchbox", { name: "Search this playlist", exact: true })
      .fill("Definitely no matching song");
    await expect(
      page.getByText("No songs match this search.", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Play playlist", exact: true }),
    ).toBeDisabled();
    await page.getByRole("button", { name: "Library", exact: true }).click();
    await expect(page.locator("#playlist-view-controls")).toBeHidden();
  } finally {
    const saved = await (
      await request.get(`/api/v1/playlists/${created.id}`)
    ).json();
    await request.delete(`/api/v1/playlists/${created.id}`, {
      data: { expected_version: saved.revision },
    });
  }
});
