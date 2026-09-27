const { test, expect } = require('@playwright/test');

test.skip(!process.env.RESONANCE_M16_E2E, 'Run against the M1.6 server with a verified enrolled root');

test('reports path-free reconciliation status while health and readiness stay separate', async ({ page, request }) => {
  const health = await request.get('/health');
  expect(health.status()).toBe(200);
  expect((await health.json()).status).toBe('ok');

  const ready = await request.get('/ready');
  expect(ready.status()).toBe(200);
  expect((await ready.json()).catalog).toBe('ready');

  const response = await request.get('/api/v1/library/status');
  expect(response.status()).toBe(200);
  const status = await response.json();
  expect(status.watcher_state).toBe('running');
  expect(status.database_state).toBe('available');
  const root = status.roots.find(item => item.id === process.env.RESONANCE_M16_ROOT_ID);
  expect(root).toBeTruthy();
  expect(root.verification_state).toBe('verified');
  expect(root.absence_reconciled).toBe(true);

  const serialized = JSON.stringify(status).toLowerCase();
  const enrolledPath = process.env.RESONANCE_M16_ROOT_PATH;
  if (enrolledPath) expect(serialized).not.toContain(enrolledPath.toLowerCase());
  expect(serialized).not.toContain('canonical_path');

  await page.goto('/');
  await expect(page.locator('body')).toBeVisible();
});
