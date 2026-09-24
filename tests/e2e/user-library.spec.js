const { test, expect } = require('@playwright/test');

test.skip(!process.env.RESONANCE_M15_E2E, 'Run against a migrated M1.5 server with Browser Song and Short.wav scanned');
test.setTimeout(60000);

async function catalogTracks(request) {
  const found = []; let cursor = null;
  do {
    const response = await request.get(`/api/v1/tracks?limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
    expect(response.ok()).toBeTruthy();
    const page = await response.json(); found.push(...page.items); cursor = page.next_cursor;
  } while (cursor);
  return found;
}
async function clearQueue(request) {
  const queue = await (await request.get('/api/v1/queue')).json();
  const response = await request.delete('/api/v1/queue', { headers: { 'Idempotency-Key': crypto.randomUUID() }, data: { expected_version: queue.revision } });
  expect(response.ok()).toBeTruthy();
}

test('natural end finalizes history and advances durable queue once', async ({ page, request }) => {
  await clearQueue(request);
  const catalog = await catalogTracks(request);
  const short = catalog.find(item => item.title === 'Short');
  const browser = catalog.find(item => item.title?.startsWith('Browser Song'));
  expect(short).toBeTruthy(); expect(browser).toBeTruthy();
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [short, browser], next_cursor: null }) }));
  await page.goto('/');
  await expect(page.locator('#items li').filter({ hasText: browser.title })).toBeVisible();
  await page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Add to queue' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(1);
  await page.locator('#items li').filter({ hasText: 'Short' }).getByRole('button', { name: 'Play now' }).click();
  await expect.poll(() => page.locator('#audio').evaluate(a => !a.paused && a.currentTime > 0.1)).toBe(true);
  const before = await (await request.get('/api/v1/queue')).json();
  expect(before.items).toHaveLength(2);
  expect(before.items.find(item => item.id === before.current_item_id).track_id).toBe(short.id);
  let reordered = false;
  await page.route('**/api/v1/queue/advance', async route => {
    if (!reordered) {
      reordered = true;
      const state = await (await request.get('/api/v1/queue')).json();
      const response = await request.put('/api/v1/queue/order', { headers: { 'Idempotency-Key': crypto.randomUUID() }, data: { item_ids: state.items.map(item => item.id), expected_version: state.revision } });
      expect(response.ok()).toBeTruthy();
    }
    await route.continue();
  });
  await expect.poll(async () => {
    const state = await (await request.get('/api/v1/queue')).json();
    return state.revision > before.revision && state.items.find(item => item.id === state.current_item_id)?.track_id === browser.id;
  }, { timeout: 15000 }).toBe(true);
  const history = await (await request.get('/api/v1/history')).json();
  expect(history.items.some(item => item.track_id === short.id && item.completed_at)).toBe(true);
  await page.reload();
  await page.getByRole('button', { name: 'Queue', exact: true }).click();
  await expect(page.locator('#items li').filter({ hasText: browser.title })).toContainText('Selected');
  expect(await page.locator('#audio').evaluate(a => a.paused)).toBe(true);
  const afterReload = await (await request.get('/api/v1/queue')).json();
  expect(reordered).toBe(true);
  expect(afterReload.revision).toBe(before.revision + 2);
  await page.locator('#player-previous').click();
  await expect.poll(async () => {
    const state = await (await request.get('/api/v1/queue')).json();
    return state.items.find(item => item.id === state.current_item_id)?.track_id;
  }).toBe(short.id);
  await expect.poll(() => page.locator('#audio').evaluate((audio, id) => !audio.paused && audio.currentSrc.includes(id), short.id)).toBe(true);
  await page.locator('#audio').evaluate(audio => audio.pause());
  await page.locator('#player-next').click();
  await expect.poll(async () => {
    const state = await (await request.get('/api/v1/queue')).json();
    return state.items.find(item => item.id === state.current_item_id)?.track_id;
  }).toBe(browser.id);
  await page.getByRole('button', { name: 'History', exact: true }).click();
  await expect(page.locator('#items li').filter({ hasText: short.title }).filter({ hasText: 'Completed' }).first()).toBeVisible();
});

test('playlist create, edit, reorder, delete, and favorite toggles persist', async ({ page, request }) => {
  const browser = (await catalogTracks(request)).find(item => item.title?.startsWith('Browser Song'));
  const playlistName = `Browser Mix ${crypto.randomUUID().slice(0, 8)}`;
  expect(browser).toBeTruthy();
  for (const item of (await catalogTracks(request)).filter(item => item.title?.startsWith('Browser Song')))
    await request.delete(`/api/v1/favorites/tracks/${item.id}`);
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [browser], next_cursor: null }) }));
  await page.goto('/');
  await page.getByRole('button', { name: 'Playlists', exact: true }).click();
  await page.locator('#playlist-name').fill(playlistName);
  await page.getByRole('button', { name: 'Create' }).click();
  await expect(page.getByRole('button', { name: playlistName })).toBeVisible();
  const playlistList = await (await request.get('/api/v1/playlists?limit=200')).json();
  const playlistID = playlistList.items.find(item => item.name === playlistName).id;
  await page.getByRole('button', { name: 'Tracks', exact: true }).click();
  await expect(page.locator('#playlist-target option', { hasText: playlistName })).toHaveCount(1);
  await page.locator('#playlist-target').selectOption({ label: playlistName });
  await page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Add to playlist' }).click();
  await expect.poll(async () => (await (await request.get(`/api/v1/playlists/${playlistID}`)).json()).items.length).toBe(1);
  await page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Add to playlist' }).click();
  await expect.poll(async () => (await (await request.get(`/api/v1/playlists/${playlistID}`)).json()).items.length).toBe(2);
  await page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: /Favorite/ }).click();
  await page.reload();
  await page.getByRole('button', { name: 'Favorites', exact: true }).click();
  await expect(page.locator('#items li').filter({ hasText: browser.title })).toBeVisible();
  await page.getByRole('button', { name: 'Playlists', exact: true }).click();
  await page.getByRole('button', { name: playlistName }).click();
  const entries = page.locator('#items li').filter({ hasText: browser.title });
  await expect(entries).toHaveCount(2);
  const beforeOrder = await (await request.get(`/api/v1/playlists/${playlistID}`)).json();
  const originalOrder = beforeOrder.items.map(item => item.id);
  await entries.nth(1).getByRole('button', { name: '↑' }).click();
  await expect.poll(async () => (await (await request.get(`/api/v1/playlists/${playlistID}`)).json()).items[0].id).toBe(originalOrder[1]);
  const renamed = `${playlistName} Updated`;
  page.once('dialog', dialog => dialog.accept(renamed));
  await page.getByRole('button', { name: 'Rename' }).click();
  await expect(page.locator('#view-title')).toContainText(renamed);
  await page.locator('#items li').filter({ hasText: browser.title }).first().getByRole('button', { name: 'Remove' }).click();
  await expect.poll(async () => (await (await request.get(`/api/v1/playlists/${playlistID}`)).json()).items.length).toBe(1);
  await page.getByRole('button', { name: 'Delete playlist' }).click();
  await expect(page.getByRole('button', { name: renamed })).toHaveCount(0);
  await page.getByRole('button', { name: 'Favorites', exact: true }).click();
  await page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Remove favorite' }).click();
  await expect(page.locator('#items li').filter({ hasText: browser.title })).toHaveCount(0);
});

test('retains one pending ended transition during outage and retries after reload', async ({ page, request }) => {
  await clearQueue(request);
  const catalog = await catalogTracks(request);
  const short = catalog.find(item => item.title === 'Short');
  const next = catalog.find(item => item.title?.startsWith('Browser Song'));
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [short, next], next_cursor: null }) }));
  await page.route('**/api/v1/queue/advance', route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'catalog_unavailable' } }) }));
  await page.goto('/');
  await page.locator('#items li').filter({ hasText: next.title }).getByRole('button', { name: 'Add to queue' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(1);
  await page.locator('#items li').filter({ hasText: 'Short' }).getByRole('button', { name: 'Play now' }).click();
  await expect.poll(async () => {
    const state = await (await request.get('/api/v1/queue')).json();
    return state.items.find(item => item.id === state.current_item_id)?.track_id;
  }).toBe(short.id);
  const before = await (await request.get('/api/v1/queue')).json();
  await expect(page.getByRole('alert')).toContainText('Queue advance was not saved', { timeout: 15000 });
  expect(await page.evaluate(() => !!sessionStorage.getItem('resonance.pending_ended'))).toBe(true);
  const still = await (await request.get('/api/v1/queue')).json();
  expect(still.revision).toBe(before.revision);
  await page.unroute('**/api/v1/queue/advance');
  await page.reload();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).revision, { timeout: 10000 }).toBe(before.revision + 1);
  expect(await page.evaluate(() => sessionStorage.getItem('resonance.pending_ended'))).toBeNull();
  const history = await (await request.get('/api/v1/history')).json();
  expect(history.items.some(item => item.track_id === short.id && item.completed_at)).toBe(true);
  expect(await page.locator('#audio').evaluate(a => a.paused)).toBe(true);
});

test('Track actions add, play next, and play now as distinct queue occurrences', async ({ page, request }) => {
  await clearQueue(request);
  const catalog = await catalogTracks(request);
  const browser = catalog.find(item => item.title?.startsWith('Browser Song'));
  const short = catalog.find(item => item.title === 'Short');
  expect(browser).toBeTruthy(); expect(short).toBeTruthy();
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [browser, short], next_cursor: null }) }));
  await page.goto('/');
  await page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Play next' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(1);
  await page.locator('#items li').filter({ hasText: short.title }).getByRole('button', { name: 'Add to queue' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(2);
  await page.locator('#items li').filter({ hasText: short.title }).getByRole('button', { name: 'Play now' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(3);
  const queue = await (await request.get('/api/v1/queue')).json();
  expect(queue.items).toHaveLength(3);
  expect(new Set(queue.items.map(item => item.id)).size).toBe(3);
  expect(queue.items.map(item => item.track_id)).toEqual([short.id, browser.id, short.id]);
  expect(queue.items.find(item => item.id === queue.current_item_id).track_id).toBe(short.id);
  await expect.poll(() => page.locator('#audio').evaluate(audio => !audio.paused && audio.currentTime > 0.1)).toBe(true);
});

test('a lost queue-add response replays its original receipt instead of adding twice', async ({ page, request }) => {
  await clearQueue(request);
  const browser = (await catalogTracks(request)).find(item => item.title?.startsWith('Browser Song'));
  expect(browser).toBeTruthy();
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [browser], next_cursor: null }) }));
  let lost = false;
  await page.route('**/api/v1/queue/items', async route => {
    if (!lost && route.request().method() === 'POST') {
      lost = true;
      await route.fetch();
      await route.abort();
      return;
    }
    await route.continue();
  });
  await page.goto('/');
  const add = page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Add to queue' });
  await expect(add).toBeVisible();
  await add.click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(1);
  await add.click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(1);
});

test('Play Now keeps its mutation token when another tab selects the same Track before response handling', async ({ page, request }) => {
  await clearQueue(request);
  const short = (await catalogTracks(request)).find(item => item.title === 'Short');
  expect(short).toBeTruthy();
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [short], next_cursor: null }) }));
  let raced = false;
  await page.route('**/api/v1/queue/items', async route => {
    if (!raced && route.request().method() === 'POST') {
      raced = true;
      const original = await route.fetch();
      const state = await (await request.get('/api/v1/queue')).json();
      const otherSelection = await request.post('/api/v1/queue/items', { headers: { 'Idempotency-Key': crypto.randomUUID() }, data: { track_id: short.id, placement: 'now', expected_version: state.revision } });
      expect(otherSelection.ok()).toBeTruthy();
      await route.fulfill({ response: original });
      return;
    }
    await route.continue();
  });
  await page.goto('/');
  const start = page.waitForResponse(response => response.url().includes('/api/v1/listening-sessions') && response.request().method() === 'POST');
  await page.locator('#items li').filter({ hasText: short.title }).getByRole('button', { name: 'Play now' }).click();
  const sessionStart = await start;
  expect(raced).toBe(true);
  expect(sessionStart.status()).toBe(409);
  await expect.poll(async () => {
    const state = await (await request.get('/api/v1/queue')).json();
    return state.items.find(item => item.id === state.current_item_id)?.track_id;
  }).toBe(short.id);
  const newer = await (await request.get('/api/v1/queue')).json();
  await expect.poll(() => page.locator('#audio').evaluate(audio => audio.ended), { timeout: 10000 }).toBe(true);
  const after = await (await request.get('/api/v1/queue')).json();
  expect(after.revision).toBe(newer.revision);
  expect(after.current_item_id).toBe(newer.current_item_id);
  expect(after.selection_token).toBe(newer.selection_token);
});

test('a queue advance response cannot autoplay a selection made by another tab afterward', async ({ page, request }) => {
  await clearQueue(request);
  const catalog = await catalogTracks(request);
  const browser = catalog.find(item => item.title?.startsWith('Browser Song'));
  const short = catalog.find(item => item.title === 'Short');
  expect(browser).toBeTruthy(); expect(short).toBeTruthy();
  const startingQueue = await (await request.get('/api/v1/queue')).json();
  const first = await request.post('/api/v1/queue/items', { headers: { 'Idempotency-Key': crypto.randomUUID() }, data: { track_id: browser.id, placement: 'now', expected_version: startingQueue.revision } });
  expect(first.ok()).toBeTruthy();
  const firstChange = await first.json();
  const second = await request.post('/api/v1/queue/items', { headers: { 'Idempotency-Key': crypto.randomUUID() }, data: { track_id: browser.id, placement: 'end', expected_version: firstChange.revision } });
  expect(second.ok()).toBeTruthy();
  await page.goto('/');
  await page.getByRole('button', { name: 'Queue', exact: true }).click();
  await page.route('**/api/v1/queue/advance', async route => {
    const response = await route.fetch();
    const state = await (await request.get('/api/v1/queue')).json();
    const changed = await request.post('/api/v1/queue/items', { headers: { 'Idempotency-Key': crypto.randomUUID() }, data: { track_id: short.id, placement: 'now', expected_version: state.revision } });
    expect(changed.ok()).toBeTruthy();
    await route.fulfill({ response });
  });
  const advanced = page.waitForResponse(response => response.url().includes('/api/v1/queue/advance') && response.request().method() === 'POST');
  await page.locator('#next').click();
  await advanced;
  await expect(page.locator('#items li').filter({ hasText: short.title })).toContainText('Selected');
  await expect.poll(() => page.locator('#audio').evaluate(audio => audio.paused)).toBe(true);
  const beforeReorder = await (await request.get('/api/v1/queue')).json();
  expect(beforeReorder.items).toHaveLength(3);
  await page.locator('#items li').first().getByRole('button', { name: '↓' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items[0].id).toBe(beforeReorder.items[1].id);
  const reordered = await (await request.get('/api/v1/queue')).json();
  await page.locator('#items li').filter({ hasText: browser.title }).first().getByRole('button', { name: 'Remove' }).click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(2);
  await page.locator('#clear-queue').click();
  await expect.poll(async () => (await (await request.get('/api/v1/queue')).json()).items.length).toBe(0);
  const cleared = await (await request.get('/api/v1/queue')).json();
  expect(cleared.selection_state).toBe('stopped');
  expect(cleared.current_item_id).toBeNull();
  expect(cleared.selection_token).toBeNull();
  expect(reordered.current_item_id).toBe(beforeReorder.current_item_id);
});

test('stale tab ended, Next, and Previous cannot change a newer queue selection', async ({ page, request }) => {
  await clearQueue(request);
  const browser = (await catalogTracks(request)).find(item => item.title?.startsWith('Browser Song'));
  expect(browser).toBeTruthy();
  const catalogPage = route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [browser], next_cursor: null }) });
  await page.route(/\/api\/v1\/tracks\?limit=50$/, catalogPage);
  await page.goto('/');
  const playNow = page.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Play now' });
  await expect(playNow).toBeVisible();
  const started = page.waitForResponse(response => response.url().includes('/api/v1/listening-sessions') && response.request().method() === 'POST');
  await playNow.click();
  await started;
  await expect.poll(() => page.locator('#audio').evaluate(audio => !audio.paused && audio.currentTime > 0.1)).toBe(true);
  const older = await (await request.get('/api/v1/queue')).json();

  const newerTab = await page.context().newPage();
  await newerTab.route(/\/api\/v1\/tracks\?limit=50$/, catalogPage);
  await newerTab.goto('/');
  await newerTab.locator('#items li').filter({ hasText: browser.title }).getByRole('button', { name: 'Play now' }).click();
  await expect.poll(async () => {
    const state = await (await request.get('/api/v1/queue')).json();
    return state.revision > older.revision && state.current_item_id !== older.current_item_id;
  }).toBe(true);
  const newer = await (await request.get('/api/v1/queue')).json();
  expect(newer.items).toHaveLength(2);

  await page.locator('#audio').evaluate(audio => { audio.currentTime = audio.duration - 0.05; });
  await expect.poll(() => page.locator('#audio').evaluate(audio => audio.ended), { timeout: 15000 }).toBe(true);
  await expect(page.locator('#player-error')).toContainText('Queue selection changed in another tab');
  let state = await (await request.get('/api/v1/queue')).json();
  expect(state.revision).toBe(newer.revision);
  expect(state.current_item_id).toBe(newer.current_item_id);
  expect(state.selection_token).toBe(newer.selection_token);

  await page.locator('#audio').evaluate(audio => { audio.currentTime = 0; });
  await page.locator('#player-next').click();
  await expect(page.locator('#status')).toContainText('Queue selection changed in another tab');
  await page.locator('#player-previous').click();
  await expect(page.locator('#status')).toContainText('Queue selection changed in another tab');
  state = await (await request.get('/api/v1/queue')).json();
  expect(state.revision).toBe(newer.revision);
  expect(state.current_item_id).toBe(newer.current_item_id);
  expect(state.selection_token).toBe(newer.selection_token);
  await newerTab.close();
});

test('seeking to the end after meaningful listening does not mark completion', async ({ page, request }) => {
  await clearQueue(request);
  const short = (await catalogTracks(request)).find(item => item.title === 'Short');
  expect(short).toBeTruthy();
  const before = await (await request.get('/api/v1/history?limit=200')).json();
  const beforeIDs = new Set(before.items.map(item => item.id));
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [short], next_cursor: null }) }));
  await page.goto('/');
  const started = page.waitForResponse(response => response.url().includes('/api/v1/listening-sessions') && response.request().method() === 'POST');
  await page.locator('#items li').filter({ hasText: short.title }).getByRole('button', { name: 'Play now' }).click();
  await started;
  await expect.poll(() => page.locator('#audio').evaluate(audio => !audio.paused && audio.currentTime > 0.1)).toBe(true);
  await page.waitForTimeout(1400);
  await page.locator('#audio').evaluate(audio => { audio.currentTime = audio.duration - 0.05; });
  await expect.poll(() => page.locator('#audio').evaluate(audio => audio.ended), { timeout: 5000 }).toBe(true);
  let newSession;
  await expect.poll(async () => {
    const history = await (await request.get('/api/v1/history?limit=200')).json();
    newSession = history.items.find(item => !beforeIDs.has(item.id) && item.track_id === short.id);
    return !!newSession?.meaningful_at;
  }).toBe(true);
  expect(newSession.completed_at).toBeNull();
});

test('decoder failure creates no listening history', async ({ page, request }) => {
  const track = (await catalogTracks(request)).find(item => item.title?.startsWith('Browser Song'));
  const before = (await (await request.get('/api/v1/history')).json()).items.length;
  await page.route(/\/api\/v1\/tracks\?limit=50$/, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [track], next_cursor: null }) }));
  await page.route('**/api/v1/tracks/*/stream', route => route.fulfill({ status: 200, contentType: 'audio/mpeg', body: 'broken media' }));
  await page.goto('/');
  await page.locator('#items li').filter({ hasText: track.title }).getByRole('button', { name: track.title }).click();
  await expect(page.getByRole('alert')).toContainText('cannot be decoded');
  const after = (await (await request.get('/api/v1/history')).json()).items.length;
  expect(after).toBe(before);
});
