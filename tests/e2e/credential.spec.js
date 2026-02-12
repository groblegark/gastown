// credential.spec.js — Credential lifecycle E2E tests via Playwright.
//
// Tests the full credential pipeline through the broker's HTTP + WebSocket APIs,
// including the device code reauth flow that requires opening an OAuth URL.
//
// Prerequisites:
//   kubectl port-forward -n gastown-next svc/gastown-next-coop-broker 18080:8080
//
// Run:
//   cd tests/e2e && npx playwright test credential.spec.js
//   cd tests/e2e && npx playwright test credential.spec.js --headed  # see the browser

const { test, expect } = require('@playwright/test');

const TOKEN = process.env.BROKER_TOKEN || 'V6T4jmuDY1GDgYDmSRaFa1wwd4RTkFKv';
const HEADERS = { 'Authorization': `Bearer ${TOKEN}` };

// ────────────────────────────────────────────────────────────
// 1. Credential Status API
// ────────────────────────────────────────────────────────────

test.describe('Credential Status API', () => {
  test('GET /api/v1/credentials/status requires auth', async ({ request }) => {
    const resp = await request.get('/api/v1/credentials/status');
    expect(resp.status()).toBe(401);
  });

  test('GET /api/v1/credentials/status returns accounts', async ({ request }) => {
    const resp = await request.get('/api/v1/credentials/status', { headers: HEADERS });
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.accounts).toBeDefined();
    expect(Array.isArray(body.accounts)).toBeTruthy();
  });

  test('at least one account is seeded', async ({ request }) => {
    const resp = await request.get('/api/v1/credentials/status', { headers: HEADERS });
    const body = await resp.json();
    expect(body.accounts.length).toBeGreaterThanOrEqual(1);
  });

  test('account has expected fields', async ({ request }) => {
    const resp = await request.get('/api/v1/credentials/status', { headers: HEADERS });
    const body = await resp.json();
    const account = body.accounts[0];
    expect(account.name).toBeTruthy();
    expect(account.provider).toBe('claude');
    expect(['healthy', 'refreshing', 'expired', 'revoked', 'static']).toContain(account.status);
    expect(typeof account.expires_in_secs).toBe('number');
  });

  test('primary account is healthy', async ({ request }) => {
    const resp = await request.get('/api/v1/credentials/status', { headers: HEADERS });
    const body = await resp.json();
    const account = body.accounts[0];
    expect(account.status).toBe('healthy');
  });

  test('token has reasonable TTL (>5 min)', async ({ request }) => {
    const resp = await request.get('/api/v1/credentials/status', { headers: HEADERS });
    const body = await resp.json();
    const account = body.accounts[0];
    // At least 5 minutes remaining
    expect(account.expires_in_secs).toBeGreaterThan(300);
  });
});

// ────────────────────────────────────────────────────────────
// 2. Credential Seed API
// ────────────────────────────────────────────────────────────

test.describe('Credential Seed API', () => {
  test('POST /api/v1/credentials/seed requires auth', async ({ request }) => {
    const resp = await request.post('/api/v1/credentials/seed', {
      data: { account: 'test' },
    });
    expect(resp.status()).toBe(401);
  });

  test('POST /api/v1/credentials/seed rejects invalid account', async ({ request }) => {
    const resp = await request.post('/api/v1/credentials/seed', {
      headers: HEADERS,
      data: {
        account: 'nonexistent-account-xyz',
        access_token: 'fake',
        refresh_token: 'fake',
        expires_in: 3600,
      },
    });
    // Should reject — account not configured
    // Accept 400 (bad request) or 404 (account not found) or 409 (conflict)
    expect([400, 404, 409, 422]).toContain(resp.status());
  });
});

// ────────────────────────────────────────────────────────────
// 3. Reauth / Device Code Flow
// ────────────────────────────────────────────────────────────

test.describe('Reauth Flow', () => {
  test('POST /api/v1/credentials/reauth requires auth', async ({ request }) => {
    const resp = await request.post('/api/v1/credentials/reauth', {
      data: { account: 'claude-max' },
    });
    expect(resp.status()).toBe(401);
  });

  test('reauth returns auth URL or rejects if healthy', async ({ request }) => {
    const resp = await request.post('/api/v1/credentials/reauth', {
      headers: HEADERS,
      data: { account: 'claude-max' },
    });

    // 200 = reauth initiated (returns auth_url + user_code)
    // 400/409 = account already healthy, reauth not needed
    expect([200, 400, 409]).toContain(resp.status());

    if (resp.status() === 200) {
      const body = await resp.json();
      expect(body.auth_url || body.user_code).toBeTruthy();

      // If auth_url present, it should be a valid Anthropic URL
      if (body.auth_url) {
        expect(body.auth_url).toContain('anthropic.com');
      }
    }
  });
});

// ────────────────────────────────────────────────────────────
// 4. WebSocket Credential Events
// ────────────────────────────────────────────────────────────

test.describe('WebSocket Credential Events', () => {
  test('WebSocket connects with credential subscription', async ({ page }) => {
    // Connect to broker WebSocket with credential event subscription
    const wsEvents = [];
    let wsConnected = false;

    page.on('websocket', ws => {
      wsConnected = true;
      ws.on('framereceived', f => {
        try {
          const msg = JSON.parse(f.payload.toString());
          wsEvents.push(msg);
        } catch (_) {}
      });
    });

    // Navigate to mux page (which establishes the WS connection)
    await page.goto(`/mux?token=${TOKEN}`, { waitUntil: 'commit' });

    // Wait for WS connection
    await page.waitForTimeout(5000);

    // The mux page should establish a WebSocket
    // We can't guarantee credential events fire during the test window,
    // but we can verify the WS connection works
    expect(wsConnected).toBeTruthy();
  });

  test('credential status visible on mux dashboard', async ({ page }) => {
    await page.goto(`/mux?token=${TOKEN}`, { waitUntil: 'commit' });

    // Wait for dashboard to load
    await page.waitForTimeout(3000);

    // The mux page should show pod status which reflects credential health
    // Pods with expired credentials show "error" or "exited" state
    const badges = page.locator('.pod-badge');
    const count = await badges.count();

    if (count > 0) {
      // At least one badge should exist
      const firstBadge = await badges.first().textContent();
      const validStates = ['starting', 'working', 'idle', 'prompt', 'error', 'exited', 'offline', 'unknown'];
      expect(validStates).toContain(firstBadge);
    }
  });
});

// ────────────────────────────────────────────────────────────
// 5. Full Reauth Simulation (headed browser)
//
// This test triggers the device code flow and opens the auth
// URL in a real browser tab. In --headed mode, a human can
// complete the OAuth flow. In headless CI, it validates the
// flow starts correctly then skips the browser auth step.
// ────────────────────────────────────────────────────────────

test.describe('Reauth Simulation', () => {
  test('trigger reauth and validate OAuth URL is navigable', async ({ request, page }) => {
    // Step 1: Trigger reauth via API
    const resp = await request.post('/api/v1/credentials/reauth', {
      headers: HEADERS,
      data: { account: 'claude-max' },
    });

    if (resp.status() === 409 || resp.status() === 400) {
      // Account already healthy — can't test reauth flow
      test.skip();
      return;
    }

    expect(resp.status()).toBe(200);
    const body = await resp.json();

    // Step 2: Validate we got an auth URL
    expect(body.auth_url).toBeTruthy();
    expect(body.auth_url).toContain('anthropic.com');

    // Step 3: Open the auth URL in the browser
    // In headed mode, this shows the Anthropic OAuth page.
    // In headless mode, we just verify it doesn't 404.
    const authResp = await page.goto(body.auth_url, {
      waitUntil: 'commit',
      timeout: 15000,
    });

    // Should get a real page (200 or 302 redirect to login)
    const status = authResp?.status() || 0;
    expect(status).toBeGreaterThanOrEqual(200);
    expect(status).toBeLessThan(500);

    // Step 4: If user_code provided, it should be a short alphanumeric code
    if (body.user_code) {
      expect(body.user_code.length).toBeGreaterThan(0);
      expect(body.user_code.length).toBeLessThan(20);
    }
  });
});

// ────────────────────────────────────────────────────────────
// 6. Credential Distribution (broker → pods)
// ────────────────────────────────────────────────────────────

test.describe('Credential Distribution', () => {
  test('broker pods API shows registered pods', async ({ request }) => {
    const resp = await request.get('/api/v1/broker/pods', { headers: HEADERS });
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.pods).toBeDefined();
    expect(body.pods.length).toBeGreaterThanOrEqual(1);
  });

  test('registered pods are healthy', async ({ request }) => {
    const resp = await request.get('/api/v1/broker/pods', { headers: HEADERS });
    const body = await resp.json();

    const healthyPods = body.pods.filter(p => p.healthy);
    expect(healthyPods.length).toBeGreaterThanOrEqual(1);
  });

  test('registered pods have coop_url', async ({ request }) => {
    const resp = await request.get('/api/v1/broker/pods', { headers: HEADERS });
    const body = await resp.json();

    for (const pod of body.pods) {
      expect(pod.coop_url).toMatch(/^http:\/\/\d+\.\d+\.\d+\.\d+:\d+$/);
      expect(pod.name).toBeTruthy();
    }
  });

  test('pods have been seen recently (<60s)', async ({ request }) => {
    const resp = await request.get('/api/v1/broker/pods', { headers: HEADERS });
    const body = await resp.json();

    for (const pod of body.pods) {
      // Health check runs every 30s, so last_seen should be <60s
      expect(pod.last_seen_secs_ago).toBeLessThan(60);
    }
  });
});
