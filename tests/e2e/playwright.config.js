// @ts-check
const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: '.',
  testMatch: '*.spec.js',
  timeout: 60000,
  expect: { timeout: 15000 },
  fullyParallel: false,
  retries: 1,
  reporter: 'list',
  use: {
    baseURL: `http://localhost:18080`,
    headless: true,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    // kubectl port-forward SPDY streams need generous timeouts —
    // rapid sequential connections exhaust the connection pool.
    navigationTimeout: 30000,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
});
