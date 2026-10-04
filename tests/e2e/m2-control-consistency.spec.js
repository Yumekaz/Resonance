const { test, expect } = require("@playwright/test");
const { AxeBuilder } = require("@axe-core/playwright");
const crypto = require("node:crypto");
test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Requires the real disposable M2 server",
);
test.use({ serviceWorkers: "block" });

async function selectSaved(request, title = "Browser Song") {
  const track = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.find((t) => t.title === title);
  const queue = await (await request.get("/api/v1/queue")).json();
  const result = await request.post("/api/v1/queue/collection", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      track_ids: [track.id],
      placement: "replace",
      expected_version: queue.revision,
    },
  });
  expect(result.ok()).toBe(true);
  return track;
}

test("mute, zero-volume recovery, media volumechange and reload stay consistent without stopping audio", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "long", exact: true }).click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#volume").press("Home");
  await page.locator("#volume").press("ArrowRight");
  await expect(page.locator("#volume")).toHaveValue("0.01");
  await page.locator("#mute-player").click();
  await expect(page.locator("#mute-player")).toHaveAccessibleName(
    "Unmute player",
  );
  expect(
    await page.locator("#audio").evaluate((a) => a.muted && !a.paused),
  ).toBe(true);
  await page.locator("#open-player").click();
  await expect(page.locator("#full-volume")).toHaveValue("0");
  await page.locator("#full-mute").click();
  await expect(page.locator("#full-volume")).toHaveValue("0.01");
  await page.locator("#full-volume").press("End");
  await expect(page.locator("#volume")).toHaveValue("1");
  // Browser-originated changes must also drive both controls, without input events.
  await page.locator("#audio").evaluate((a) => {
    a.volume = 0.6;
    a.muted = true;
  });
  await expect(page.locator("#full-mute")).toHaveAccessibleName(
    "Unmute player",
  );
  await expect(page.locator("#volume")).toHaveValue("0");
  await page.reload();
  await expect(page.locator("#mute-player")).toHaveAccessibleName(
    "Unmute player",
  );
  expect(
    await page.locator("#audio").evaluate((a) => ({
      volume: a.volume,
      muted: a.muted,
      paused: a.paused,
    })),
  ).toEqual({ volume: 0.6, muted: true, paused: true });
  await page.locator("#mute-player").click();
  await expect(page.locator("#volume")).toHaveValue("0.6");
  await page.locator("#volume").press("Home");
  await page.locator("#mute-player").click();
  await expect(page.locator("#volume")).toHaveValue("0.6");
});

test("a platform that rejects player gain shows device guidance and retains mute", async ({
  page,
}) => {
  // Capability fault injection, not evidence of physical iOS/Android behavior.
  await page.addInitScript(() =>
    Object.defineProperty(HTMLMediaElement.prototype, "volume", {
      configurable: true,
      get: () => 1,
      set: () => {},
    }),
  );
  await page.goto("/");
  await expect(page.locator("#volume")).toBeHidden();
  await page.locator("#open-player").click();
  await expect(page.locator("#full-volume")).toBeHidden();
  await expect(page.locator("#volume-note")).toHaveText(
    "Use your device buttons to change volume",
  );
  await page.locator("#full-mute").click();
  expect(await page.locator("#audio").evaluate((a) => a.muted)).toBe(true);
});

test("Sort has selected-state styling, keyboard selection, dismissal and actual catalog ordering", async ({
  page,
}) => {
  await page.goto("/#tracks");
  await page.locator("#catalog-order-trigger").press("ArrowDown");
  await expect(
    page.getByRole("menuitemradio", { name: "A–Z", exact: true }),
  ).toBeFocused();
  await expect(
    page.getByRole("menuitemradio", { name: "A–Z", exact: true }),
  ).toHaveAttribute("aria-checked", "true");
  const axe = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"])
    .analyze();
  expect(axe.violations).toEqual([]);
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  await expect(page.locator("#catalog-order-trigger")).toHaveAccessibleName(
    "Sort your collection: Z–A",
  );
  const expected = (
    await (
      await page.request.get("/api/v1/tracks?limit=50&order=title_desc")
    ).json()
  ).items.map((t) => t.title);
  await expect
    .poll(() => page.locator("#items .item-title").allTextContents())
    .toEqual(expected);
  await page.locator("#catalog-order-trigger").click();
  await expect(
    page.getByRole("menuitemradio", { name: "Z–A", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.locator("#catalog-order-trigger")).toBeFocused();
  await page.locator("#catalog-order-trigger").click();
  await page.keyboard.press("Tab");
  await expect(page.locator(".select-menu")).toHaveCount(0);
  await page.locator("#catalog-order-trigger").click();
  await page.locator("#view-title").click();
  await expect(page.locator(".select-menu")).toHaveCount(0);
});

test("saved selection can favorite, open album and add to a playlist without creating playback history", async ({
  page,
  request,
}) => {
  const track = await selectSaved(request);
  await request.delete(`/api/v1/favorites/tracks/${track.id}`);
  const qBefore = await (await request.get("/api/v1/queue")).json();
  const history = await (await request.get("/api/v1/history?limit=200")).json();
  const playlistName = "Control review " + crypto.randomUUID().slice(0, 8);
  const created = await request.post("/api/v1/playlists", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      name: playlistName,
      expected_version: 0,
    },
  });
  expect(created.ok()).toBe(true);
  const playlist = await created.json();
  try {
    await page.goto("/#tracks");
    await page.locator("#open-player").click();
    await expect(page.locator("#full-album")).toHaveText("Browser Album");
    await expect(page.locator("#full-favorite")).toBeEnabled();
    await page.locator("#full-favorite").click();
    await expect(page.locator("#full-favorite")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await page.locator("#full-playlist").click();
    await page.getByRole("button", { name: /^Choose playlist:/ }).click();
    await page
      .getByRole("menuitemradio", { name: playlistName, exact: true })
      .click();
    await page.getByRole("button", { name: "Add track", exact: true }).click();
    await expect(page.locator("#action-dialog")).toBeHidden();
    await expect(page.locator("#full-playlist")).toBeFocused();
    const detail = await (
      await request.get(`/api/v1/playlists/${playlist.id}`)
    ).json();
    expect(detail.items.map((i) => i.track_id)).toEqual([track.id]);
    await page.locator("#full-album").click();
    await expect(page.locator("#view-title")).toHaveText("Browser Album");
    const after = await (await request.get("/api/v1/queue")).json();
    expect(after.revision).toBe(qBefore.revision);
    expect(after.selection_token).toBe(qBefore.selection_token);
    expect(
      await (await request.get("/api/v1/history?limit=200")).json(),
    ).toEqual(history);
    expect(
      await page.locator("#audio").evaluate((a) => a.paused && !a.currentSrc),
    ).toBe(true);
  } finally {
    await request.delete(`/api/v1/favorites/tracks/${track.id}`);
    const latest = await (
      await request.get(`/api/v1/playlists/${playlist.id}`)
    ).json();
    await request.delete(`/api/v1/playlists/${playlist.id}`, {
      data: { expected_version: latest.revision },
    });
  }
});

test("immediate keyboard dismissal of a track menu restores its action button", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/#tracks");
  const anchor = page
    .locator("#items li")
    .filter({ hasText: "Browser Song" })
    .getByRole("button", { name: /^More actions for / });
  for (let attempt = 0; attempt < 3; attempt++) {
    await anchor.click();
    await page.keyboard.press("Escape");
    await expect(page.locator(".context-menu")).toHaveCount(0);
    await expect(anchor).toBeFocused();
  }
});

test("rotating during artwork expansion cancels obsolete geometry without pausing playback", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "long", exact: true }).click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#open-player").click();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator("#now-playing")).toHaveAttribute(
    "data-transitioning",
    "false",
  );
  await expect(page.locator("#player-art-transition")).toBeHidden();
  expect(
    await page
      .locator("#full-art")
      .evaluate((a) => getComputedStyle(a).visibility),
  ).toBe("visible");
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
});

test("a delayed saved-song response cannot replace newer player metadata or action targets", async ({
  page,
  request,
}) => {
  const first = await selectSaved(request);
  let release, began;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const requested = new Promise((resolve) => {
    began = resolve;
  });
  await page.route(`**/api/v1/tracks/${first.id}`, async (route) => {
    const response = await route.fetch();
    began();
    await gate;
    await route.fulfill({ response }).catch(() => {});
  });
  await page.goto("/#queue");
  await requested;
  await page.locator("#open-player").click();
  const second = await selectSaved(request, "long");
  await page.locator("#full-queue-refresh").click();
  await expect(page.locator("#full-title")).toHaveText(second.title);
  await expect(page.locator("#full-favorite")).toHaveAccessibleName(
    /^(Favorite|Remove favorite): long$/,
  );
  release();
  await page.waitForLoadState("networkidle");
  await expect(page.locator("#full-favorite")).toBeEnabled();
  await expect(page.locator("#full-title")).toHaveText("long");
  await expect(page.locator("#full-art")).toHaveAttribute(
    "data-sleeve-key",
    `track:${second.id}`,
  );
  await page.locator("#full-playlist").click();
  await expect(page.locator("#dialog-content > p").first()).toContainText(
    "long",
  );
  expect(
    await page.locator("#audio").evaluate((a) => a.paused && !a.currentSrc),
  ).toBe(true);
});

for (const size of [
  { width: 320, height: 568 },
  { width: 390, height: 844 },
  { width: 430, height: 932 },
  { width: 844, height: 390 },
  { width: 768, height: 1024 },
  { width: 1101, height: 900 },
  { width: 1440, height: 900 },
])
  test(`favorite placement and controls fit at ${size.width}×${size.height}`, async ({
    page,
    request,
  }) => {
    await selectSaved(request);
    await page.setViewportSize(size);
    await page.goto("/#tracks");
    await page.locator("#open-player").click();
    await expect(page.locator("#full-favorite")).toBeEnabled();
    const geometry = await page.evaluate(() => {
      const rect = (id) => document.getElementById(id).getBoundingClientRect();
      const dialog = document.getElementById("now-playing");
      const controls = document
        .querySelector(".full-controls")
        .getBoundingClientRect();
      return {
        favoriteAboveSeek: rect("full-favorite").bottom < rect("full-seek").top,
        overflow: dialog.scrollWidth > dialog.clientWidth,
        controlsFit: [
          ...document.querySelectorAll(".full-controls button"),
        ].every((button) => {
          const r = button.getBoundingClientRect();
          return r.left >= controls.left - 1 && r.right <= controls.right + 1;
        }),
      };
    });
    expect(geometry).toEqual({
      favoriteAboveSeek: true,
      overflow: false,
      controlsFit: true,
    });
    await page.locator("#full-mute").scrollIntoViewIfNeeded();
    await page.locator("#full-mute").click();
    expect(await page.locator("#audio").evaluate((a) => a.muted)).toBe(true);
    await page.locator("#close-player").click();
    await page.locator("#catalog-order-trigger").click();
    const menu = await page.locator(".select-menu").boundingBox();
    expect(menu.x).toBeGreaterThanOrEqual(0);
    expect(menu.x + menu.width).toBeLessThanOrEqual(size.width);
  });
