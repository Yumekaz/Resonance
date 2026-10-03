const { test, expect } = require("@playwright/test");
const http = require("node:http");
test.skip(
  !process.env.RESONANCE_M2_E2E,
  "Run against the real M2 journey server",
);

test("manifest, static-only shell cache and offline server state", async ({
  page,
  context,
  request,
}) => {
  const manifest = await (await request.get("/manifest.webmanifest")).json();
  expect(manifest.name).toBe("Resonance");
  expect(manifest.display).toBe("standalone");
  expect(manifest.icons.map((i) => i.sizes)).toEqual(["192x192", "512x512"]);
  for (const icon of manifest.icons)
    expect((await request.get(icon.src)).status()).toBe(200);
  await page.goto("/");
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.reload();
  await expect
    .poll(() => page.evaluate(() => !!navigator.serviceWorker.controller))
    .toBe(true);
  await page.goto("/#tracks");
  await page.getByRole("button", { name: "long", exact: true }).click();
  await expect
    .poll(() => page.locator("#audio").evaluate((a) => a.currentTime > 0.1))
    .toBe(true);
  const cached = await page.evaluate(async () => {
    const out = [];
    for (const key of await caches.keys())
      for (const r of await (await caches.open(key)).keys())
        out.push(new URL(r.url).pathname);
    return out;
  });
  expect(cached).toContain("/library.html");
  expect(
    cached.some(
      (p) =>
        p.startsWith("/api/") ||
        p.startsWith("/media/") ||
        p.startsWith("/admin"),
    ),
  ).toBe(false);
  await context.setOffline(true);
  await page.reload();
  await expect(
    page.getByText("Resonance server unavailable", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Home", exact: true }),
  ).toBeVisible();
  expect(await page.locator("#audio").evaluate((a) => a.paused)).toBe(true);
  await context.setOffline(false);
  await expect(page.locator("#connection-state")).toHaveText(
    "Library connected",
  );
});

test("a shell update waits for old clients and activates a coherent new shell", async ({
  browser,
  baseURL,
}) => {
  // Only the release marker is varied. All assets and APIs come from the real
  // Go server; this isolated origin lets us exercise two releases deterministically.
  let release = 1;
  const server = http.createServer(async (req, res) => {
    try {
      const response = await fetch(baseURL + req.url);
      let body = Buffer.from(await response.arrayBuffer());
      if (req.url === "/sw.js")
        body = Buffer.from(
          body
            .toString()
            .replace("resonance-shell-", `resonance-shell-update-${release}-`),
        );
      if (req.url === "/" || req.url === "/library.html")
        body = Buffer.from(
          body
            .toString()
            .replace(
              "<head>",
              `<head><meta name="release-marker" content="${release}">`,
            ),
        );
      res.writeHead(response.status, {
        "Content-Type": response.headers.get("content-type") || "text/plain",
        "Cache-Control": "no-store",
      });
      res.end(body);
    } catch {
      res.writeHead(503);
      res.end();
    }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const context = await browser.newContext();
  let page = await context.newPage();
  try {
    await page.goto(origin);
    await page.evaluate(() => navigator.serviceWorker.ready);
    await page.reload();
    await expect
      .poll(() => page.evaluate(() => !!navigator.serviceWorker.controller))
      .toBe(true);
    expect(
      await page.locator('meta[name="release-marker"]').getAttribute("content"),
    ).toBe("1");
    release = 2;
    await page.evaluate(async () => {
      const registration = await navigator.serviceWorker.getRegistration();
      await registration.update();
    });
    await expect
      .poll(() =>
        page.evaluate(
          async () =>
            !!(await navigator.serviceWorker.getRegistration()).waiting,
        ),
      )
      .toBe(true);
    await page.reload();
    expect(
      await page.locator('meta[name="release-marker"]').getAttribute("content"),
    ).toBe("1");
    // Closing a tab does not synchronously prove Chromium has released its
    // worker client. Opening the next tab immediately can keep release 1 alive.
    // Observe activation on the waiting worker without creating another client.
    let nextWorker;
    for (const worker of context.serviceWorkers()) {
      if (
        (await worker.evaluate(() => VERSION)).startsWith(
          "resonance-shell-update-2-",
        )
      )
        nextWorker = worker;
    }
    expect(nextWorker).toBeTruthy();
    await page.close();
    await expect
      .poll(
        () =>
          nextWorker.evaluate(
            async () =>
              self.registration.waiting === null &&
              self.registration.active?.state === "activated" &&
              !(await caches.keys()).some((key) =>
                key.startsWith("resonance-shell-update-1-"),
              ),
          ),
        { timeout: 10000 },
      )
      .toBe(true);
    page = await context.newPage();
    await page.goto(origin);
    await expect
      .poll(() =>
        page.locator('meta[name="release-marker"]').getAttribute("content"),
      )
      .toBe("2");
    const keys = await page.evaluate(() => caches.keys());
    expect(keys.some((k) => k.startsWith("resonance-shell-update-1-"))).toBe(
      false,
    );
  } finally {
    await context.close();
    await new Promise((resolve) => server.close(resolve));
  }
});
