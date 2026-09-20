const { test, expect } = require('@playwright/test');

test('plays before full transfer and seeks through a new partial response', async ({ page, request }, testInfo) => {
  const probe = await request.head('/media/demo-track');
  expect(probe.status()).toBe(200);
  const mediaSize = Number(probe.headers()['content-length']);
  expect(mediaSize).toBeGreaterThan(20 * 1024 * 1024);
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Network.enable');
  await cdp.send('Network.setCacheDisabled', { cacheDisabled: true });
  await cdp.send('Network.emulateNetworkConditions', {
    offline: false, latency: 20, downloadThroughput: 256 * 1024, uploadThroughput: -1
  });
  const mediaRequests = new Set();
  let transferred = 0;
  cdp.on('Network.responseReceived', event => {
    if (new URL(event.response.url).pathname === '/media/demo-track') mediaRequests.add(event.requestId);
  });
  cdp.on('Network.dataReceived', event => {
    if (mediaRequests.has(event.requestId)) transferred += event.dataLength;
  });
  await page.goto('/');
  await expect(page.getByText('Server reachable')).toBeVisible();
  await expect(page.locator('#track-title')).toHaveText('Demo Track');
  await expect(page.locator('#seek')).toBeEnabled();
  await page.getByRole('button', { name: 'Play' }).click();
  await expect.poll(() => page.locator('#audio').evaluate(a => !a.paused && a.currentTime > 0.1)).toBe(true);
  const bytesAtPlayback = transferred;
  expect(bytesAtPlayback).toBeGreaterThan(0);
  expect(bytesAtPlayback).toBeLessThan(mediaSize);
  await page.getByRole('button', { name: 'Pause' }).click();
  await expect.poll(() => page.locator('#audio').evaluate(a => a.paused)).toBe(true);
  const target = await page.locator('#audio').evaluate(a => a.duration * 0.7);
  const targetBuffered = await page.locator('#audio').evaluate((a, target) => {
    for (let i = 0; i < a.buffered.length; i++) {
      if (a.buffered.start(i) <= target && a.buffered.end(i) >= target) return true;
    }
    return false;
  }, target);
  expect(targetBuffered).toBe(false);
  // Only requests issued after this point can satisfy the seek assertion.
  const afterSeek = new Set();
  page.on('request', req => afterSeek.add(req));
  const partialResponse = page.waitForResponse(response => {
    if (!afterSeek.has(response.request()) || new URL(response.url()).pathname !== '/media/demo-track' || response.status() !== 206) return false;
    const requested = /^bytes=(\d+)-/.exec(response.request().headers().range || '');
    const returned = /^bytes (\d+)-(\d+)\/(\d+)$/.exec(response.headers()['content-range'] || '');
    return requested && returned && Math.abs(Number(requested[1]) - mediaSize * 0.7) < 65536 && requested[1] === returned[1] && Number(returned[3]) === mediaSize && Number(response.headers()['content-length']) === Number(returned[2]) - Number(returned[1]) + 1;
  });
  await page.evaluate(() => {
    window.seekStarted = performance.now();
    window.seekPlaying = new Promise(resolve => document.querySelector('#audio').addEventListener('playing',
      () => resolve(performance.now() - window.seekStarted), { once: true }));
  });
  await page.locator('#seek').evaluate(el => {
    el.value = '700'; el.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await page.getByRole('button', { name: 'Play' }).click();
  const response = await partialResponse;
  const seekToPlayingMs = await page.evaluate(() => window.seekPlaying);
  // Setting currentTime alone is not evidence that playback resumed.
  await expect.poll(() => page.locator('#audio').evaluate((a, target) =>
    !a.paused && !a.seeking && a.currentTime > target + 0.2, target)).toBe(true);
  const evidence = { mediaSize, bytesAtPlayback, target, targetBuffered,
    seekRequest: response.request().headers().range,
    seekContentRange: response.headers()['content-range'], seekToPlayingMs,
    downloadBytesPerSecond: 256 * 1024, latencyMs: 20 };
  console.log(JSON.stringify(evidence));
  await testInfo.attach('streaming-evidence', { body: JSON.stringify(evidence, null, 2), contentType: 'application/json' });
});

test('shows an unreachable server state', async ({ page }) => {
  await page.route('**/health', route => route.abort());
  await page.goto('/');
  await expect(page.getByText('Server unreachable')).toBeVisible();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Play' })).toBeDisabled();
});

test('shows a corrupt media error', async ({ page }) => {
  await page.route('**/media/demo-track', route => route.fulfill({
    status: 200, contentType: 'audio/wav', body: 'not an audio file'
  }));
  await page.goto('/');
  await expect(page.getByRole('alert')).toContainText('Audio is unavailable');
});
