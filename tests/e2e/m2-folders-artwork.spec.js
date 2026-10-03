const { test, expect } = require("@playwright/test");
const fs = require("node:fs");
const path = require("node:path");

test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Requires a disposable real M2 server",
);
test.use({ serviceWorkers: "block" });
const adminURL =
  process.env.RESONANCE_ADMIN_E2E_BASE_URL || "http://127.0.0.1:8081";

test("host add, verify and scan release controls; original artwork wins; disabled folders leave discovery", async ({
  page,
  request,
}) => {
  test.setTimeout(60000);
  const parent = path.resolve("data", "m2", "folder-regression");
  fs.mkdirSync(parent, { recursive: true });
  const folder = fs.mkdtempSync(path.join(parent, "cover-"));
  const title = "Original cover regression " + path.basename(folder);
  const png = Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/dXcAAAAASUVORK5CYII=",
    "base64",
  );
  function frame(name, data) {
    const header = Buffer.alloc(10);
    header.write(name);
    header.writeUInt32BE(data.length, 4);
    return Buffer.concat([header, data]);
  }
  // A real tagged MP3 with a valid PNG mislabelled JPEG, not a mocked API.
  const frames = Buffer.concat([
    frame("TIT2", Buffer.concat([Buffer.from([3]), Buffer.from(title)])),
    frame(
      "APIC",
      Buffer.concat([
        Buffer.from([0]),
        Buffer.from("image/jpeg\0"),
        Buffer.from([3, 0]),
        png,
      ]),
    ),
  ]);
  const n = frames.length;
  const header = Buffer.from([
    73,
    68,
    51,
    3,
    0,
    0,
    (n >>> 21) & 127,
    (n >>> 14) & 127,
    (n >>> 7) & 127,
    n & 127,
  ]);
  fs.writeFileSync(
    path.join(folder, "cover.mp3"),
    Buffer.concat([
      header,
      frames,
      fs.readFileSync("testdata/metadata/untagged.mp3"),
    ]),
  );
  let rootID;
  try {
    await page.goto(adminURL + "/");
    await page.getByRole("button", { name: "Add library folder" }).click();
    await page.getByLabel("Display name", { exact: true }).fill(title);
    await page
      .getByLabel("Full folder path on this computer", { exact: true })
      .fill(folder);
    await page.getByRole("button", { name: "Add folder", exact: true }).click();
    const root = page.locator(".folder").filter({ hasText: title });
    await expect(root).toBeVisible();
    await expect(root.getByRole("button", { name: "Scan now" })).toBeEnabled();
    await root.getByRole("button", { name: "Scan now" }).click();
    await expect(page.locator("#message")).toContainText("Scan succeeded");
    await expect(
      root.getByRole("button", { name: "Verify", exact: true }),
    ).toBeEnabled();
    await root.getByRole("button", { name: "Verify", exact: true }).click();
    await page.locator("#confirm-verify").click();
    await expect(page.locator("#verify-dialog")).not.toBeVisible();
    await expect(root.getByRole("button", { name: "Scan now" })).toBeEnabled();
    const roots = await (
      await request.get(adminURL + "/api/v1/admin/roots")
    ).json();
    rootID = roots.items.find((r) => r.name === title).id;
    const search = await (
      await request.get("/api/v1/search?q=" + encodeURIComponent(title))
    ).json();
    expect(search.tracks).toHaveLength(1);
    const track = search.tracks[0];
    const artwork = await request.get(track.artwork_url);
    expect(artwork.status()).toBe(200);
    expect(artwork.headers()["content-type"]).toBe("image/png");
    expect(await artwork.body()).toEqual(png);
    await page.goto("/#search");
    await page.getByRole("searchbox").fill(title);
    const row = page.locator("#items .track-row").filter({ hasText: title });
    await expect(row.locator("img")).toHaveAttribute("src", track.artwork_url);
    await expect(row.locator("img")).toHaveAttribute("data-missing", "false");
    await page.getByRole("button", { name: title, exact: true }).click();
    await expect(page.locator("#cover")).toHaveAttribute(
      "src",
      track.artwork_url,
    );
    await page.locator("#open-player").click();
    await expect(page.locator("#full-art")).toHaveAttribute(
      "src",
      track.artwork_url,
    );
    await expect
      .poll(() =>
        page
          .locator("#full-art")
          .evaluate((i) => i.complete && i.naturalWidth > 0),
      )
      .toBe(true);
    await page.locator("#close-player").click();
    await page.goto(adminURL + "/");
    await root.getByRole("button", { name: "Disable", exact: true }).click();
    await expect(root).toContainText("Hidden from Library and Search");
    await expect(
      root.getByRole("button", { name: "Enable", exact: true }),
    ).toBeEnabled();
    const hidden = await (
      await request.get("/api/v1/search?q=" + encodeURIComponent(title))
    ).json();
    expect(hidden.tracks).toHaveLength(0);
    const retained = await (
      await request.get("/api/v1/tracks/" + track.id)
    ).json();
    expect(retained.available).toBe(false);
  } finally {
    if (rootID) {
      const response = await request.post(
        adminURL + "/api/v1/admin/roots/" + rootID + "/disable",
        { headers: { "X-Resonance-Admin": "1", Origin: adminURL }, data: {} },
      );
      expect(response.status()).toBe(200);
    }
  }
});
