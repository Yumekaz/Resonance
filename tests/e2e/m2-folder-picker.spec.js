const { test, expect } = require("@playwright/test");
const { AxeBuilder } = require("@axe-core/playwright");
test.skip(!process.env.RESONANCE_M2_E2E, "Requires real M2 host server");
const host =
  process.env.RESONANCE_ADMIN_E2E_BASE_URL || "http://127.0.0.1:8081";
// Only the native-dialog result transport is controlled. Real selection and
// cancellation are separately verified with the owner in the actual Windows UI.
async function open(page) {
  await page.goto(host + "/");
  await page
    .getByRole("button", { name: "Add library folder", exact: true })
    .click();
}
test("chooser fills form with Unicode name without enrolling; cancel retains selection", async ({
  page,
}) => {
  const before = await (
    await page.request.get(host + "/api/v1/admin/roots")
  ).json();
  let picks = 0;
  const name = "音楽".repeat(40);
  await page.route("**/api/v1/admin/folder-picker", (route) =>
    route.fulfill({
      json:
        ++picks === 1
          ? { path: "C:\\Fixture music Ω", name, cancelled: false }
          : { path: "", name: "", cancelled: true },
    }),
  );
  await open(page);
  await expect(page.locator("#choose-folder")).toBeFocused();
  await expect(page.locator("#confirm-add-folder")).toBeDisabled();
  await page.locator("#choose-folder").click();
  await expect(page.locator("#chosen-folder")).toHaveText(
    "C:\\Fixture music Ω",
  );
  const filled = await page.locator("#folder-name").inputValue();
  expect(new TextEncoder().encode(filled).length).toBeLessThanOrEqual(128);
  expect(filled.length).toBeGreaterThan(0);
  await expect(page.locator("#confirm-add-folder")).toBeEnabled();
  await page.locator("#choose-folder").click();
  await expect(page.locator("#picker-status")).toContainText(
    "Selection cancelled",
  );
  await expect(page.locator("#chosen-folder")).toHaveText(
    "C:\\Fixture music Ω",
  );
  expect(await page.locator("#folder-name").inputValue()).toBe(filled);
  expect(
    await (await page.request.get(host + "/api/v1/admin/roots")).json(),
  ).toEqual(before);
  const axe = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"])
    .analyze();
  expect(axe.violations).toEqual([]);
});
test("closing a pending choice aborts it and cannot fill a reopened form", async ({
  page,
}) => {
  let release;
  const gate = new Promise((resolve) => (release = resolve));
  let calls = 0;
  await page.route("**/api/v1/admin/folder-picker", async (route) => {
    calls++;
    await gate;
    await route
      .fulfill({
        json: {
          path: "C:\\Late folder",
          name: "Late folder",
          cancelled: false,
        },
      })
      .catch(() => {});
  });
  await open(page);
  await page.locator("#folder-name").fill("My mix");
  await page.locator("#choose-folder").click();
  await expect(page.locator("#choose-folder")).toBeDisabled();
  await page.locator("#choose-folder").dispatchEvent("click");
  await page.locator("#cancel-add").click();
  await expect(page.locator("#add-folder")).toBeHidden();
  await expect(page.locator("#show-add")).toBeEnabled();
  release();
  await page
    .getByRole("button", { name: "Add library folder", exact: true })
    .click();
  await expect(page.locator("#chosen-folder")).toBeHidden();
  await expect(page.locator("#folder-name")).toHaveValue("My mix");
  await expect(page.locator("#picker-status")).not.toContainText(
    "Windows window",
  );
  expect(calls).toBe(1);
});

test("suggested names follow a replacement folder while an edited name stays yours", async ({
  page,
}) => {
  let count = 0;
  const names = ["Music", "Albums", "Pop"];
  await page.route("**/api/v1/admin/folder-picker", (route) => {
    const name = names[count++];
    return route.fulfill({
      json: { path: `C:\\${name}`, name, cancelled: false },
    });
  });
  await open(page);
  await page.locator("#choose-folder").click();
  await expect(page.locator("#folder-name")).toHaveValue("Music");
  await page.locator("#choose-folder").click();
  await expect(page.locator("#folder-name")).toHaveValue("Albums");
  await page.locator("#folder-name").fill("Road trip");
  await page.locator("#choose-folder").click();
  await expect(page.locator("#chosen-folder")).toHaveText("C:\\Pop");
  await expect(page.locator("#folder-name")).toHaveValue("Road trip");
});
test("unsupported platform and picker failures provide manual fallback", async ({
  page,
}) => {
  await page.route("**/api/v1/admin/status", async (route) => {
    const response = await route.fetch();
    const state = await response.json();
    state.folder_picker = { available: false };
    await route.fulfill({ response, json: state });
  });
  await open(page);
  await expect(page.locator("#choose-folder")).toBeHidden();
  await expect(page.locator("#manual-folder")).toHaveAttribute("open", "");
  await expect(page.locator("#folder-name")).toBeFocused();
  await page.locator("#folder-path").fill("C:\\Manual folder");
  await expect(page.locator("#confirm-add-folder")).toBeEnabled();
  await page.unroute("**/api/v1/admin/status");
  await page.route("**/api/v1/admin/folder-picker", (route) =>
    route.fulfill({
      status: 408,
      json: { error: { code: "folder_picker_timeout" } },
    }),
  );
  await open(page);
  await page.locator("#choose-folder").click();
  await expect(page.locator("#picker-status")).toContainText("timed out");
  await expect(page.locator("#choose-folder")).toBeEnabled();
});
