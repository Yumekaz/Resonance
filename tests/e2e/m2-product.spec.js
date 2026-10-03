const { test, expect } = require("@playwright/test");
const { trackAction, addToPlaylist } = require("./m2-ui");
const adminURL =
  process.env.RESONANCE_ADMIN_E2E_BASE_URL || "http://127.0.0.1:8081";
test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Run against the real M2 journey server",
);
test.use({ serviceWorkers: "block" });

test("grouped real search, keyboard access, empty and no-result states", async ({
  page,
  request,
}) => {
  await page.goto("/#search");
  await expect(page.getByText("What would you like to hear?")).toBeVisible();
  const input = page.getByRole("searchbox");
  await input.fill("Browser");
  await expect(page.locator("#status")).toContainText("matches");
  for (const text of ["Browser Song", "Browser Artist", "Browser Album"])
    await expect(
      page.locator("#items").getByRole("button", { name: text, exact: true }),
    ).toBeVisible();
  await input.press("ArrowDown");
  await expect(
    page.getByRole("button", { name: "Browser Song", exact: true }),
  ).toBeFocused();
  await page
    .getByRole("button", { name: "Browser Artist", exact: true })
    .click();
  await expect(page.locator("#view-title")).toHaveText("Browser Artist");
  await page.getByRole("button", { name: "View tracks", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Browser Song", exact: true }),
  ).toBeVisible();
  await expect(page.locator("#library-tabs")).toBeHidden();
  await page.getByRole("button", { name: "Library", exact: true }).click();
  await page.getByRole("button", { name: "Albums", exact: true }).click();
  await page
    .getByRole("button", { name: "Browser Album", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Play album", exact: true }),
  ).toBeVisible();
  await page.keyboard.press("/");
  await input.fill("There is no such song 123456789");
  await expect(page.getByText("No matches this time.")).toBeVisible();
  const response = await request.get("/api/v1/search?q=Browser&limit=1");
  expect(response.status()).toBe(200);
  const data = await response.json();
  expect(data.tracks).toHaveLength(1);
  expect(data.artists).toHaveLength(1);
  expect(data.albums).toHaveLength(1);
  expect(JSON.stringify(data)).not.toMatch(
    /canonical_path|relative_path|root_id/,
  );
});

test("late search and catalog responses cannot replace a newer view", async ({
  page,
}) => {
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  let seen;
  const started = new Promise((resolve) => (seen = resolve));
  await page.route("**/api/v1/search?q=slow*", async (route) => {
    seen();
    await gate;
    await route
      .fulfill({
        json: {
          tracks: [
            {
              id: "trk_" + "1".repeat(32),
              title: "STALE RESULT",
              available: false,
            },
          ],
          artists: [],
          albums: [],
        },
      })
      .catch(() => {});
  });
  await page.goto("/#search");
  await page.getByRole("searchbox").fill("slow");
  await started;
  await page.getByRole("searchbox").fill("Browser");
  await expect(
    page
      .locator("#items")
      .getByRole("button", { name: "Browser Song", exact: true }),
  ).toBeVisible();
  release();
  await expect(page.getByText("STALE RESULT")).toHaveCount(0);
  await page.getByRole("button", { name: "Home", exact: true }).click();
  await expect(page.locator("#view-title")).toHaveText("Back to the music.");
  await expect(page.locator("#items")).toBeEmpty();
});

test("mobile player persists through navigation, menus and full Now Playing", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "long", exact: true }).click();
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.1),
    )
    .toBe(true);
  const source = await page.locator("#audio").getAttribute("src");
  await page.getByRole("button", { name: "Home", exact: true }).click();
  await page.getByRole("button", { name: "Library", exact: true }).click();
  await expect(page.locator("#audio")).toHaveAttribute("src", source);
  await expect
    .poll(() =>
      page.locator("#audio").evaluate((a) => !a.paused && a.currentTime > 0.2),
    )
    .toBe(true);
  await page.getByRole("button", { name: "Open Now Playing" }).click();
  await expect(page.getByRole("dialog", { name: "long" })).toBeVisible();
  await expect(
    page.getByRole("slider", { name: "Seek in Now Playing" }),
  ).toBeEnabled();
  await page.locator("#full-play").click();
  await expect
    .poll(() => page.locator("#audio").evaluate((a) => a.paused))
    .toBe(true);
  await page.keyboard.press("Escape");
  await expect(page.locator("#now-playing")).not.toBeVisible();
  await expect(page.locator("#open-player")).toBeFocused();
  await page.getByRole("button", { name: "Tracks", exact: true }).click();
  const row = page.locator("#items li").filter({ hasText: "Browser Song" });
  await row.getByRole("button", { name: /^More actions for / }).click();
  await expect(page.getByRole("menu")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(
    row.getByRole("button", { name: /^More actions for / }),
  ).toBeFocused();
  await page.getByRole("button", { name: "App options", exact: true }).click();
  await page
    .locator("#action-dialog")
    .getByRole("button", { name: "About this app", exact: true })
    .click();
  await expect(page.locator("#action-dialog")).toContainText(
    "Music needs a connection",
  );
  await page.keyboard.press("Escape");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});

test("host is separate, status is real, and local root controls work", async ({
  page,
  request,
}) => {
  for (const path of ["/api/v1/admin/roots", "/admin/"])
    expect((await request.get(path)).status()).toBe(404);
  await page.goto("/");
  await expect(page.locator("body")).not.toContainText("Library folders");
  await expect(page.locator('a[href*="8081"]')).toHaveCount(0);
  await page.goto(adminURL + "/");
  await expect(
    page.getByRole("heading", { name: "A home for your library." }),
  ).toBeVisible();
  const root = page.locator(".folder").filter({ hasText: "Listening room" });
  await expect(root).toContainText("Verified");
  await root.getByRole("button", { name: "Disable", exact: true }).click();
  await expect(root).toContainText("Disabled");
  await root.getByRole("button", { name: "Enable", exact: true }).click();
  await expect(root).toContainText("Enabled");
  await root.getByRole("button", { name: "Scan now", exact: true }).click();
  await expect(page.locator("#message")).toContainText("Scan succeeded", {
    timeout: 30000,
  });
  await root.getByRole("button", { name: "Verify", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("trusted source");
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  const blocked = await request.post(adminURL + "/api/v1/admin/roots", {
    headers: { Origin: "http://attacker.example", "X-Resonance-Admin": "1" },
    data: { name: "Denied", path: "C:\\private" },
  });
  expect(blocked.status()).toBe(403);
});

test("error and broken-artwork states remain understandable", async ({
  page,
}) => {
  await page.route("**/api/v1/search?*", (route) =>
    route.fulfill({
      status: 503,
      json: { error: { code: "catalog_unavailable" } },
    }),
  );
  await page.goto("/#search");
  await page.getByRole("searchbox").fill("Browser");
  await expect(page.locator("#status")).toContainText("Search is unavailable");
  await page.route("**/ready", (route) => route.abort());
  await page.reload();
  await expect(
    page.getByText("Resonance server unavailable", { exact: true }),
  ).toBeVisible();
  await page.route("**/api/v1/tracks?limit=50", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "trk_" + "2".repeat(32),
            title: "Long Unicode 雪 ".repeat(30),
            artist_credit: "Artist ".repeat(100),
            available: false,
            artwork_url: "/broken-cover",
            stream_url: "/not-used",
          },
        ],
        next_cursor: null,
      },
    }),
  );
  await page.goto("/#tracks");
  await expect(page.locator("#items .item-title")).toBeDisabled();
  await expect(page.locator("#items img")).toHaveAttribute(
    "src",
    /\/assets\/(artwork-unavailable|sleeve-[a-z0-9-]+)\.webp$/,
  );
  await expect(page.locator("#items img")).toHaveAttribute(
    "data-missing",
    "true",
  );
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});
