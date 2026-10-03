const { test, expect } = require("@playwright/test");
const crypto = require("node:crypto");
test.skip(!process.env.RESONANCE_M2_E2E, "Real M2 server required");
test.use({ serviceWorkers: "block" });

test("album Back returns to the artist context it came from", async ({
  page,
}) => {
  await page.goto("/#artists");
  await page
    .getByRole("button", { name: "Browser Artist", exact: true })
    .press("Enter");
  await page
    .getByRole("button", { name: "Browser Album", exact: true })
    .press("Enter");
  await page
    .getByRole("button", { name: "Back to Browser Artist", exact: true })
    .press("Enter");
  await expect(page.locator("#view-title")).toHaveText("Browser Artist");
  await expect(
    page.getByRole("button", { name: "Browser Album", exact: true }),
  ).toBeVisible();
});

test("opening a long queue starts on the page containing its saved selection", async ({
  page,
  request,
}) => {
  let q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  const track = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.find((t) => t.title === "long");
  q = await (await request.get("/api/v1/queue")).json();
  const added = await request.post("/api/v1/queue/collection", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      track_ids: Array(201).fill(track.id),
      placement: "end",
      expected_version: q.revision,
    },
  });
  expect(added.status()).toBe(201);
  q = await (await request.get("/api/v1/queue")).json();
  const selectedID = q.items[150].id;
  const select = await request.post("/api/v1/queue/select", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { item_id: selectedID, expected_version: q.revision },
  });
  expect(select.status()).toBe(200);
  const metadataRequests = [];
  page.on("request", (r) => {
    if (r.url().endsWith(`/api/v1/tracks/${track.id}`))
      metadataRequests.push(r.url());
  });
  await page.goto("/#queue");
  await expect(
    page.locator(`#items [data-item-id="${selectedID}"]`),
  ).toBeVisible();
  await expect(page.locator("#previous-page")).toBeVisible();
  await expect(page.locator("#items .track-row")).toHaveCount(51);
  await expect(page.locator("#items .track-row").first()).toHaveAttribute(
    "data-item-id",
    selectedID,
  );
  await page.locator("#previous-page").click();
  await expect(page.locator("#items .track-row")).toHaveCount(100);
  await page.locator("#items .track-row").last().scrollIntoViewIfNeeded();
  await expect
    .poll(() =>
      page
        .locator("#items .track-row")
        .last()
        .locator("img")
        .getAttribute("data-pending"),
    )
    .toBe("false");
  expect(metadataRequests.length).toBeLessThanOrEqual(3);
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(true);
  q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
});

test("keyboard queue removal focuses the next occurrence or the empty destination", async ({
  page,
  request,
}) => {
  const q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  const tracks = (await (await request.get("/api/v1/tracks?limit=50")).json())
    .items;
  const next = await (await request.get("/api/v1/queue")).json();
  const added = await request.post("/api/v1/queue/collection", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      track_ids: [
        tracks.find((t) => t.title === "long").id,
        tracks.find((t) => t.title === "Browser Song").id,
      ],
      placement: "end",
      expected_version: next.revision,
    },
  });
  expect(added.status()).toBe(201);
  await page.goto("/#queue");
  await page
    .locator("#items .track-row")
    .first()
    .getByRole("button", { name: /^More actions for/ })
    .press("Enter");
  await page
    .getByRole("menuitem", { name: "Remove", exact: true })
    .press("Enter");
  await expect(page.locator("#items .track-row")).toHaveCount(1);
  await expect(
    page
      .locator("#items .track-row")
      .getByRole("button", { name: /^More actions for Browser Song/ }),
  ).toBeFocused();
  await page
    .locator("#items .track-row")
    .getByRole("button", { name: /^More actions for/ })
    .press("Enter");
  await page
    .getByRole("menuitem", { name: "Remove", exact: true })
    .press("Enter");
  await expect(page.locator("#items .track-row")).toHaveCount(0);
  await expect(page.locator("#view-title")).toBeFocused();
});

test("late queue confirmation cannot replace the newly opened queue count", async ({
  page,
  request,
}) => {
  const q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  let release, observed;
  const gate = new Promise((r) => {
      release = r;
    }),
    started = new Promise((r) => {
      observed = r;
    });
  await page.route("**/api/v1/queue/items", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    observed();
    await gate;
    await route.continue();
  });
  await page.goto("/#tracks");
  const row = page.locator("#items .track-row").filter({
    has: page.getByRole("button", { name: "Browser Song", exact: true }),
  });
  await row.getByRole("button", { name: /^More actions for/ }).click();
  await page
    .getByRole("menuitem", { name: "Add to queue", exact: true })
    .click();
  await started;
  await page.locator("#open-queue").click();
  release();
  await expect(page.locator("#items .track-row")).toHaveCount(1);
  await expect(page.locator("#status")).toHaveText("1 song in your queue");
  expect(
    (await (await request.get("/api/v1/queue")).json()).items,
  ).toHaveLength(1);
});

test("phone Up Next keeps the first upcoming song above the persistent dock", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "long", exact: true }).click();
  const row = page.locator("#items .track-row").filter({
    has: page.getByRole("button", { name: "Browser Song", exact: true }),
  });
  await row.getByRole("button", { name: /^More actions for/ }).click();
  await page.getByRole("menuitem", { name: "Play next", exact: true }).click();
  await page.locator("#open-queue").click();
  const upcoming = page.locator("#items .track-row").filter({
    has: page.getByRole("button", { name: "Browser Song", exact: true }),
  });
  await expect(upcoming).toBeVisible();
  const box = await upcoming.boundingBox(),
    dock = await page.locator(".mini-player").boundingBox();
  expect(box.y + box.height).toBeLessThanOrEqual(dock.y);
  await page
    .getByRole("button", { name: "Open player", exact: true })
    .press("Enter");
  await expect(page.locator("#now-playing")).toBeVisible();
  await page.keyboard.press("Escape");
  await page.locator("#play-toggle").click();
});

test("empty Up Next has a direct music path and no meaningless queue controls", async ({
  page,
  request,
}) => {
  const q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  await page.goto("/#queue");
  await expect(
    page.getByRole("heading", {
      name: "Your next song starts here.",
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.locator("#queue-controls")).toBeHidden();
  await expect(page.locator("#listening-summary")).toBeEmpty();
  await page
    .getByRole("button", { name: "Find a song", exact: true })
    .press("Enter");
  await expect(page.locator("#view-title")).toHaveText("Search");
  await expect(page.getByRole("searchbox")).toBeFocused();
});

test("empty saved queue preserves a solo song and its source while browsing", async ({
  page,
  request,
}) => {
  const q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  await page.goto("/#tracks");
  const row = page
    .locator("#items .track-row")
    .filter({ has: page.getByRole("button", { name: "long", exact: true }) });
  await row.getByRole("button", { name: /^More actions for/ }).click();
  await page
    .getByRole("menuitem", { name: "Play this song only", exact: true })
    .click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  const source = await page.locator("#audio").evaluate((a) => a.currentSrc);
  await page.locator("#open-queue").click();
  await expect(page.locator("#listening-summary")).toContainText("long");
  await expect(page.locator("#listening-summary")).toContainText(
    "Playing one song",
  );
  await expect(page.locator("#queue-controls")).toBeHidden();
  await page.getByRole("button", { name: "Find a song", exact: true }).click();
  expect(await page.locator("#audio").evaluate((a) => a.currentSrc)).toBe(
    source,
  );
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(false);
  expect(
    (await (await request.get("/api/v1/queue")).json()).items,
  ).toHaveLength(0);
  await page.locator("#play-toggle").click();
});
test("playlist detail survives reload and browser Back restores the overview", async ({
  page,
  request,
}) => {
  const name = `Quality route ${crypto.randomUUID().slice(0, 8)}`;
  const created = await request.post("/api/v1/playlists", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { name, expected_version: 0 },
  });
  expect(created.status()).toBe(201);
  const playlist = await created.json();
  await page.goto("/#playlists");
  await page.getByRole("button", { name, exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`#playlists/${playlist.id}$`));
  await page.reload();
  await expect(page.locator("#view-title")).toHaveText(name);
  await expect(page.locator("#playlist-back")).toBeVisible();
  await expect(page.locator("#view-title")).toBeFocused();
  await page.goBack();
  await expect(page.locator("#view-title")).toHaveText("Playlists");
  await expect(page.locator("#view-title")).toBeFocused();
  const detail = await (
    await request.get(`/api/v1/playlists/${playlist.id}`)
  ).json();
  await request.delete(`/api/v1/playlists/${playlist.id}`, {
    data: { expected_version: detail.revision },
  });
});
test("rapid catalog Play requests serialize and only the latest intent starts audio", async ({
  page,
  request,
}) => {
  const before = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: before.revision },
  });
  let release,
    seen,
    writes = 0;
  const gate = new Promise((r) => (release = r)),
    started = new Promise((r) => (seen = r));
  const shown = (
    await (await request.get("/api/v1/tracks?limit=50")).json()
  ).items.filter((track) => track.available);
  await page.route("**/api/v1/queue/collection", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    writes++;
    if (writes === 1) {
      seen();
      await gate;
    }
    await route.continue();
  });
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "Browser Song", exact: true }).click();
  await started;
  await page.getByRole("button", { name: "long", exact: true }).click();
  expect(writes).toBe(1);
  release();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await expect(page.locator("#now-title")).toHaveText("long");
  const q = await (await request.get("/api/v1/queue")).json();
  expect(q.items).toHaveLength(shown.length);
  expect(q.items.map((item) => item.track_id)).toEqual(
    shown.map((track) => track.id),
  );
  expect(q.items.find((i) => i.id === q.current_item_id).title).toBe("long");
  await page.locator("#play-toggle").click();
});
test("album cards retain their credit and cover without per-card metadata requests", async ({
  page,
}) => {
  const requests = [];
  page.on("request", (r) => requests.push(r.url()));
  await page.goto("/#albums");
  const card = page.getByRole("button", { name: "Browser Album", exact: true });
  await expect(card.locator(".cover-credit")).toContainText("Browser Artist");
  await expect(card.locator("img")).toHaveAttribute("data-pending", "false");
  expect(
    requests.filter((url) =>
      /\/api\/v1\/albums\/[^/]+\/tracks\?limit=12/.test(url),
    ),
  ).toHaveLength(0);
});
test("favorite membership is bounded to visible tracks and playlists are not fetched at startup", async ({
  page,
}) => {
  const requests = [];
  page.on("request", (r) =>
    requests.push({ url: r.url(), body: r.postData() }),
  );
  await page.goto("/#tracks");
  await expect(page.locator("#items .favorite-button").first()).toBeEnabled();
  const lookups = requests.filter((r) =>
    r.url.endsWith("/api/v1/favorites/lookup"),
  );
  expect(lookups.length).toBeGreaterThan(0);
  for (const r of lookups)
    expect(JSON.parse(r.body).track_ids.length).toBeLessThanOrEqual(50);
  expect(
    requests.filter((r) => r.url.includes("/api/v1/favorites?limit=200")),
  ).toHaveLength(0);
  expect(
    requests.filter((r) => r.url.includes("/api/v1/playlists?")),
  ).toHaveLength(0);
});
test("end of queue has a truthful replay state and replay retains its occurrence", async ({
  page,
  request,
}) => {
  let q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  await page.goto("/#tracks");
  const row = page
    .locator("#items .track-row")
    .filter({ has: page.getByRole("button", { name: "Short", exact: true }) });
  await row.getByRole("button", { name: /^More actions for/ }).click();
  await page.getByRole("menuitem", { name: "Play now", exact: true }).click();
  await expect
    .poll(() => page.locator("#audio").evaluate((a) => a.ended), {
      timeout: 15000,
    })
    .toBe(true);
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).selection_state,
    )
    .toBe("stopped");
  await page.locator("#open-player").click();
  await expect(page.locator("#listening-context")).toContainText(
    "End of your queue",
  );
  q = await (await request.get("/api/v1/queue")).json();
  await page.locator("#full-play").click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  const replayed = await (await request.get("/api/v1/queue")).json();
  expect(replayed.items).toHaveLength(1);
  expect(replayed.current_item_id).toBe(q.current_item_id);
  expect(replayed.selection_token).not.toBeNull();
  await page.locator("#full-play").click();
});

test("playlist song picker adds duplicate occurrences without losing the playlist context", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const name = `Quality playlist ${crypto.randomUUID().slice(0, 8)}`;
  await page.goto("/#playlists");
  await page.getByRole("button", { name: "New playlist", exact: true }).click();
  await page
    .getByRole("textbox", { name: "Playlist name", exact: true })
    .fill(name);
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.getByRole("button", { name, exact: true }).click();
  await expect(page.locator("#library-tabs")).toBeHidden();
  await expect(page.locator("#collection-links")).toBeHidden();
  await page.getByRole("button", { name: "Add songs", exact: true }).click();
  await page
    .getByLabel("Find a song in your library", { exact: true })
    .fill("Browser Song");
  const add = page
    .locator(".picker-results")
    .getByRole("button", { name: /^Add Browser Song by/ })
    .first();
  await add.click();
  await expect(page.locator(".picker-results button").first()).toHaveText(
    "Add again",
  );
  await add.click();
  await page.keyboard.press("Escape");
  await expect(page.locator("#items .track-row")).toHaveCount(2);
  await expect(
    page.getByRole("button", { name: "Add songs", exact: true }),
  ).toBeFocused();
  const id = (
    await (await request.get("/api/v1/playlists?limit=200")).json()
  ).items.find((p) => p.name === name).id;
  const detail = await (await request.get(`/api/v1/playlists/${id}`)).json();
  expect(detail.items[0].id).not.toBe(detail.items[1].id);
  const before = await (await request.get("/api/v1/queue")).json();
  await page
    .getByRole("button", { name: "Play playlist", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).items.length,
    )
    .toBe(before.items.length + 2);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#play-toggle").click();
  await page
    .getByRole("button", { name: "Delete playlist", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Keep playlist", exact: true })
    .click();
  await expect(page.locator("#view-title")).toHaveText(name);
  expect((await request.get(`/api/v1/playlists/${id}`)).status()).toBe(200);
  await page
    .getByRole("button", { name: "Delete playlist", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Delete permanently", exact: true })
    .click();
  await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "New playlist", exact: true }),
  ).toBeFocused();
});

test("all phone collection destinations are visible and category navigation never scrolls", async ({
  page,
}) => {
  for (const size of [
    { width: 320, height: 568 },
    { width: 390, height: 844 },
    { width: 430, height: 932 },
    { width: 844, height: 390 },
    { width: 768, height: 1024 },
  ]) {
    await page.setViewportSize(size);
    await page.goto("/#albums");
    await expect(page.locator("#items > li").first()).toBeVisible();
    for (const name of ["Tracks", "Artists", "Albums"])
      await expect(
        page
          .locator("#library-tabs")
          .getByRole("button", { name, exact: true }),
      ).toBeVisible();
    expect(
      await page
        .locator("#library-tabs")
        .evaluate((el) => el.scrollWidth <= el.clientWidth),
    ).toBe(true);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await expect(
      page
        .locator(".sidebar")
        .getByRole("button", { name: "Up Next", exact: true }),
    ).toBeVisible();
    if (size.width <= 700) {
      for (const name of ["Favorites", "Playlists", "History"])
        await expect(
          page
            .locator("#collection-links")
            .getByRole("button", { name, exact: true }),
        ).toBeVisible();
      await expect(page.locator("#open-queue")).toBeVisible();
    }
  }
});

test("playlist creation is contextual, keeps invalid input, and returns keyboard focus", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto("/#playlists");
  const opener = page.getByRole("button", {
    name: "New playlist",
    exact: true,
  });
  await expect(page.locator("#playlist-name")).toBeHidden();
  await opener.press("Enter");
  const input = page.getByRole("textbox", {
    name: "Playlist name",
    exact: true,
  });
  await expect(input).toBeFocused();
  await input.fill("   ");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.locator("#create-playlist-error")).toBeVisible();
  await expect(input).toHaveValue("   ");
  await expect(page.locator("#create-playlist-dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(opener).toBeFocused();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});

test("track menu preserves context, supports arrows and restores keyboard focus", async ({
  page,
}) => {
  await page.goto("/#tracks");
  const row = page
    .locator("#items .track-row")
    .filter({ has: page.getByRole("button", { name: "long", exact: true }) });
  const more = row.getByRole("button", { name: /More actions for long by/ });
  await more.focus();
  await more.press("Enter");
  const menu = page.getByRole("menu", { name: "long", exact: true });
  await expect(menu).toBeVisible();
  await expect(
    menu.getByRole("menuitem", { name: "Play now", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(
    menu.getByRole("menuitem", { name: "Play next", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await expect(more).toBeFocused();
  await expect(page.locator("#action-dialog")).not.toBeVisible();
});

test("single-song listening agrees with Up Next and selecting an occurrence does not duplicate it", async ({
  page,
  request,
}) => {
  let q = await (await request.get("/api/v1/queue")).json();
  await request.delete("/api/v1/queue", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { expected_version: q.revision },
  });
  const catalog = await (await request.get("/api/v1/tracks?limit=50")).json();
  const long = catalog.items.find((t) => t.title === "long");
  const short = catalog.items.find((t) => t.title === "Short");
  q = await (await request.get("/api/v1/queue")).json();
  const batch = await request.post("/api/v1/queue/collection", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: {
      track_ids: [short.id, long.id, short.id],
      placement: "now",
      expected_version: q.revision,
    },
  });
  expect(batch.status()).toBe(201);
  await page.goto("/#tracks");
  const soloRow = page
    .locator("#items .track-row")
    .filter({ has: page.getByRole("button", { name: "long", exact: true }) });
  await soloRow.getByRole("button", { name: /^More actions for/ }).click();
  await page
    .getByRole("menuitem", { name: "Play this song only", exact: true })
    .click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  await page.locator("#open-queue").click();
  await expect(page.locator("#view-title")).toHaveText("Up Next");
  await expect(page.locator("#listening-summary")).toContainText(
    "Playing one song",
  );
  await expect(page.locator("#listening-summary")).toContainText(
    "Continue with Short",
  );
  q = await (await request.get("/api/v1/queue")).json();
  await page.getByRole("button", { name: "Resume queue", exact: true }).click();
  await expect(page.locator("#listening-summary")).toContainText(
    "Playing from your queue",
  );
  expect(
    (await (await request.get("/api/v1/queue")).json()).items,
  ).toHaveLength(3);
  await page
    .locator(`#items [data-item-id="${q.items[1].id}"] .item-title`)
    .click();
  await expect
    .poll(
      async () =>
        (await (await request.get("/api/v1/queue")).json()).current_item_id,
    )
    .toBe(q.items[1].id);
  expect(
    (await (await request.get("/api/v1/queue")).json()).items,
  ).toHaveLength(3);
  await page.locator("#play-toggle").click();
});
