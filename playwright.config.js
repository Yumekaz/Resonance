const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './tests/e2e',
  timeout: 30000,
  use: { baseURL: process.env.RESONANCE_E2E_BASE_URL || 'http://127.0.0.1:8080', browserName: 'chromium', channel: 'chrome' }
});
