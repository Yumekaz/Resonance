const { test, expect } = require('@playwright/test');

test.skip(!process.env.RESONANCE_M17_E2E, 'Run against a fresh M1.7 catalog with the generated journey corpus');
test.setTimeout(90000);

async function tracks(request) {
  const result = [];
  let cursor = null;
  do {
    const suffix = cursor ? `&cursor=${encodeURIComponent(cursor)}` : '';
    const response = await request.get(`/api/v1/tracks?limit=200${suffix}`);
    expect(response.ok()).toBeTruthy();
    const page = await response.json();
    result.push(...page.items);
    cursor = page.next_cursor;
  } while (cursor);
  return result;
}

async function clearQueue(request) {
  const queue = await (await request.get('/api/v1/queue')).json();
  const response = await request.delete('/api/v1/queue', {
    headers: { 'Idempotency-Key': crypto.randomUUID() },
    data: { expected_version: queue.revision }
  });
  expect(response.ok()).toBeTruthy();
}

test('real catalog journey persists queue, playlist, favorite and qualifying history', async ({ page, request }) => {
  await clearQueue(request);
  const catalog = await tracks(request);
  const browser = catalog.find(item => item.title === 'Browser Song' && item.artist_credit === 'Browser Artist');
  const short = catalog.find(item => item.title === 'Short');
  const long = catalog.find(item => item.title === 'long');
  expect(browser).toBeTruthy();
  expect(short).toBeTruthy();
  expect(long).toBeTruthy();

  const actualResponses = new Map();
  page.on('response', response => {
    const path = new URL(response.url()).pathname;
    if (path.startsWith('/api/v1/') || path.endsWith('/stream')) actualResponses.set(path, (actualResponses.get(path) || 0) + 1);
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Artists', exact: true }).click();
  await page.getByRole('button', { name: 'Browser Artist', exact: true }).click();
  await page.getByRole('button', { name: 'Browser Album', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Browser Song', exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Tracks', exact: true }).click();
  const browserRow = page.locator('#items li').filter({ hasText: 'Browser Song' });
  const shortRow = page.locator('#items li').filter({ hasText: 'Short' });
  await expect(browserRow).toBeVisible();
  await expect(shortRow).toBeVisible();
  await browserRow.getByRole('button', { name: 'Add to queue' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(1);
  const beforePlayNow = await (await request.get('/api/v1/queue')).json();
  expect(new Set(beforePlayNow.items.map(item => item.id)).size).toBe(1);

  await shortRow.getByRole('button', { name: 'Play now' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(2);
  const afterPlayNow = await (await request.get('/api/v1/queue')).json();
  expect(new Set(afterPlayNow.items.map(item => item.id)).size).toBe(2);
  await expect.poll(() => page.locator('#audio').evaluate(audio => !audio.paused && audio.currentTime > 0.1)).toBe(true);
  await expect.poll(async () => {
    const queue = await (await request.get('/api/v1/queue')).json();
    return queue.items.find(item => item.id === queue.current_item_id)?.track_id;
  }).toBe(short.id);
  await expect.poll(async () => {
    const queue = await (await request.get('/api/v1/queue')).json();
    return queue.items.length;
  }).toBe(2);

  await expect.poll(async () => {
    const history = await (await request.get('/api/v1/history')).json();
    return history.items.some(item => item.track_id === short.id && item.completed_at);
  }, { timeout: 15000 }).toBe(true);
  await expect.poll(async () => {
    const queue = await (await request.get('/api/v1/queue')).json();
    return queue.items.find(item => item.id === queue.current_item_id)?.track_id;
  }, { timeout: 15000 }).toBe(browser.id);

  await page.locator('#player-previous').click();
  await expect.poll(async () => {
    const queue = await (await request.get('/api/v1/queue')).json();
    return queue.items.find(item => item.id === queue.current_item_id)?.track_id;
  }).toBe(short.id);
  await page.locator('#player-next').click();
  await expect.poll(async () => {
    const queue = await (await request.get('/api/v1/queue')).json();
    return queue.items.find(item => item.id === queue.current_item_id)?.track_id;
  }).toBe(browser.id);

  const playlistName = `M17 Journey ${crypto.randomUUID().slice(0, 8)}`;
  await page.getByRole('button', { name: 'Playlists', exact: true }).click();
  await page.locator('#playlist-name').fill(playlistName);
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(page.getByRole('button', { name: playlistName, exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Tracks', exact: true }).click();
  await page.locator('#playlist-target').selectOption({ label: playlistName });
  const currentBrowserRow = page.locator('#items li').filter({ hasText: 'Browser Song' });
  await currentBrowserRow.getByRole('button', { name: 'Add to playlist' }).click();
  await currentBrowserRow.getByRole('button', { name: 'Add to playlist' }).click();
  await page.getByRole('button', { name: 'Playlists', exact: true }).click();
  await page.getByRole('button', { name: playlistName, exact: true }).click();
  const playlistEntries = page.locator('#items li').filter({ hasText: 'Browser Song' });
  await expect(playlistEntries).toHaveCount(2);
  const detailBefore = await (await request.get('/api/v1/playlists?limit=200')).json();
  const playlist = detailBefore.items.find(item => item.name === playlistName);
  expect(playlist).toBeTruthy();
  const playlistDetailBefore = await (await request.get(`/api/v1/playlists/${playlist.id}`)).json();
  const originalOccurrenceIDs = playlistDetailBefore.items.map(item => item.id);
  expect(new Set(originalOccurrenceIDs).size).toBe(2);
  await playlistEntries.nth(1).getByRole('button', { name: '↑' }).click();
  await expect.poll(async () => (await (await request.get(`/api/v1/playlists/${playlist.id}`)).json()).items[0].id).toBe(originalOccurrenceIDs[1]);
  const renamed = `${playlistName} Updated`;
  page.once('dialog', dialog => dialog.accept(renamed));
  await page.getByRole('button', { name: 'Rename' }).click();
  await expect(page.locator('#view-title')).toContainText(renamed);

  await page.getByRole('button', { name: 'Tracks', exact: true }).click();
  await page.locator('#items li').filter({ hasText: 'Browser Song' }).getByRole('button', { name: '♡ Favorite' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/favorites')).json()).items.some(item => item.track_id === browser.id)).toBe(true);
  await page.reload();
  await page.getByRole('button', { name: 'Favorites', exact: true }).click();
  await expect(page.locator('#items li').filter({ hasText: 'Browser Song' })).toBeVisible();
  await page.locator('#items li').filter({ hasText: 'Browser Song' }).getByRole('button', { name: 'Remove favorite' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/favorites')).json()).items.some(item => item.track_id === browser.id)).toBe(false);

  await page.getByRole('button', { name: 'History', exact: true }).click();
  await expect(page.locator('#items li').filter({ hasText: 'Short' }).filter({ hasText: 'Completed' }).first()).toBeVisible();
  await page.reload();
  await page.getByRole('button', { name: 'Queue', exact: true }).click();
  await expect(page.locator('#items li').filter({ hasText: 'Browser Song' })).toContainText('Selected');
  expect(await page.locator('#audio').evaluate(audio => audio.paused)).toBe(true);

  const actualTrackList = (actualResponses.get('/api/v1/tracks') || 0) > 0;
  const actualQueueWrite = (actualResponses.get('/api/v1/queue/items') || 0) > 0;
  const actualSession = [...actualResponses.keys()].some(path => path.startsWith('/api/v1/listening-sessions'));
  expect(actualTrackList).toBe(true);
  expect(actualQueueWrite).toBe(true);
  expect(actualSession).toBe(true);

  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: 'Playlists', exact: true }).click();
  await page.getByRole('button', { name: renamed, exact: true }).click();
  await page.getByRole('button', { name: 'Delete playlist', exact: true }).click();
  await clearQueue(request);
});
