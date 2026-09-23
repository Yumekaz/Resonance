const { test, expect } = require('@playwright/test');

test.skip(!process.env.RESONANCE_M14_E2E, 'Run against a migrated, scanned M1.4 server');

test('browses Artist and Album, plays real Track before full download, and seeks by 206', async ({ page, request }, testInfo) => {
  let cursor = null;
  let long = null;
  do {
    const url = cursor ? `/api/v1/tracks?limit=50&cursor=${encodeURIComponent(cursor)}` : '/api/v1/tracks?limit=50';
    const list = await (await request.get(url)).json();
    long = list.items.find(item => item.title === 'long');
    cursor = list.next_cursor;
  } while (!long && cursor);
  expect(long).toBeTruthy();
  const size = Number((await request.head(long.stream_url)).headers()['content-length']);
  expect(size).toBeGreaterThan(20 * 1024 * 1024);

  await page.goto('/');
  await page.getByRole('button', { name: 'Artists', exact: true }).click();
  await page.getByRole('button', { name: 'Browser Artist' }).click();
  await expect(page.getByRole('button', { name: 'Browser Album' })).toBeVisible();
  await page.getByRole('button', { name: 'Browser Album' }).click();
  await expect(page.getByRole('button', { name: 'Browser Song' })).toBeVisible();
  await page.getByRole('button', { name: 'Tracks', exact: true }).click();
  await expect(page.locator('#status')).toContainText('shown');
  for (let pageIndex = 0; pageIndex < 10 && !await page.getByRole('button', { name: 'long', exact: true }).count(); pageIndex++) {
    const before = await page.locator('#items li').count();
    const loadMore = page.getByRole('button', { name: 'Load more' });
    await expect(loadMore).toBeVisible();
    await loadMore.click();
    await expect.poll(() => page.locator('#items li').count()).toBeGreaterThan(before);
  }
  await expect(page.getByRole('button', { name: 'long', exact: true })).toBeVisible();

  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Network.enable');
  await cdp.send('Network.setCacheDisabled', { cacheDisabled: true });
  await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 20, downloadThroughput: 256 * 1024, uploadThroughput: -1 });
  const requests = new Set();
  let transferred = 0;
  cdp.on('Network.responseReceived', event => { if (new URL(event.response.url).pathname === long.stream_url) requests.add(event.requestId); });
  cdp.on('Network.dataReceived', event => { if (requests.has(event.requestId)) transferred += event.dataLength; });
  await page.getByRole('button', { name: 'long', exact: true }).click();
  await expect.poll(() => page.locator('#audio').evaluate(a => !a.paused && a.currentTime > 0.1)).toBe(true);
  const bytesAtPlayback = transferred;
  expect(bytesAtPlayback).toBeGreaterThan(0);
  expect(bytesAtPlayback).toBeLessThan(size);
  const target = await page.locator('#audio').evaluate(a => a.duration * 0.7);
  await page.locator('#audio').evaluate(a => a.pause());
  const afterSeek = new Set();
  page.on('request', req => afterSeek.add(req));
  const partial = page.waitForResponse(response => afterSeek.has(response.request()) && new URL(response.url()).pathname === long.stream_url && response.status() === 206);
  await page.evaluate(() => {
    window.seekStarted = performance.now();
    window.seekPlaying = new Promise(resolve => document.querySelector('#audio').addEventListener('playing', () => resolve(performance.now() - window.seekStarted), { once: true }));
  });
  await page.locator('#audio').evaluate((a, t) => { a.currentTime = t; a.play(); }, target);
  const response = await partial;
  const seekToPlayingMs = await page.evaluate(() => window.seekPlaying);
  await expect.poll(() => page.locator('#audio').evaluate((a, t) => !a.seeking && a.currentTime > t, target)).toBe(true);
  const evidence = { size, bytesAtPlayback, seekRange: response.request().headers().range, seekContentRange: response.headers()['content-range'], seekToPlayingMs };
  console.log(JSON.stringify(evidence));
  await testInfo.attach('catalog-playback-evidence', { body: JSON.stringify(evidence, null, 2), contentType: 'application/json' });
});

test('shows an unavailable Track without starting playback', async ({ page }) => {
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 'trk_11111111111111111111111111111111', title: 'Offline Song', artist_credit: null, album_title: null, available: false, stream_url: '/api/v1/tracks/trk_11111111111111111111111111111111/stream' }], next_cursor: null }) }));
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'Offline Song' })).toBeDisabled();
  await expect(page.getByText('Unavailable')).toBeVisible();
  await expect(page.locator('#audio')).not.toHaveAttribute('src');
});

test('reports a browser decoder failure for an indexed Track', async ({ page }) => {
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [{ id: 'trk_22222222222222222222222222222222', title: 'Browser Song', artist_credit: 'Browser Artist', album_title: 'Browser Album', available: true, stream_url: '/api/v1/tracks/trk_22222222222222222222222222222222/stream' }], next_cursor: null }) }));
  await page.route('**/api/v1/tracks/*/stream', route => route.fulfill({ status: 200, contentType: 'audio/mpeg', body: 'not an audio file' }));
  await page.goto('/');
  await page.getByRole('button', { name: 'Browser Song' }).click();
  await expect(page.getByRole('alert')).toContainText('cannot be decoded');
});
