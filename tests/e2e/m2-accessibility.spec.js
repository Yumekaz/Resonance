const { test, expect } = require("@playwright/test");
const { AxeBuilder } = require("@axe-core/playwright");
test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Run against the real M2 journey server",
);
test.use({ serviceWorkers: "block" });
test.setTimeout(90000);

for (const width of [390, 1440])
  test(`accessible listener navigation and player at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const evidence = [];
    for (const view of [
      "home",
      "tracks",
      "search",
      "queue",
      "playlists",
      "favorites",
      "history",
    ]) {
      await page.goto("/#" + view);
      await expect(page.locator("#connection-state")).toHaveText(
        "Library connected",
      );
      const result = await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"])
        .analyze();
      evidence.push({
        view,
        violations: result.violations,
        incomplete: result.incomplete.map((x) => x.id),
      });
      expect(result.violations, JSON.stringify(result.violations)).toEqual([]);
    }
    await page.goto("/#tracks");
    await page.getByRole("button", { name: "long", exact: true }).click();
    await page.locator("#open-player").click();
    const player = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"])
      .analyze();
    expect(player.violations, JSON.stringify(player.violations)).toEqual([]);
    await page.keyboard.press("Escape");
    await expect(page.locator("#open-player")).toBeFocused();
    await page.emulateMedia({ reducedMotion: "reduce" });
    expect(
      await page.evaluate(
        () => matchMedia("(prefers-reduced-motion: reduce)").matches,
      ),
    ).toBe(true);
    await testInfo.attach("accessibility-evidence", {
      body: JSON.stringify(evidence, null, 2),
      contentType: "application/json",
    });
  });

test("host setup is labeled and keyboard accessible", async ({ page }) => {
  await page.goto(
    (process.env.RESONANCE_ADMIN_E2E_BASE_URL || "http://127.0.0.1:8081") + "/",
  );
  await page.locator(".folder").first().waitFor();
  await page.getByRole("button", { name: "Add library folder" }).click();
  await expect(page.getByLabel("Display name", { exact: true })).toBeFocused();
  const result = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"])
    .analyze();
  expect(result.violations, JSON.stringify(result.violations)).toEqual([]);
});
