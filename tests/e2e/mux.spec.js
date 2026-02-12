// Mux dashboard E2E tests — runs against a live coop broker with registered pods.
//
// Prerequisites:
//   kubectl port-forward -n gastown-next svc/gastown-next-coop-broker 18080:8080
//   At least 1 agent pod registered
//
// Run:
//   cd tests/e2e && npx playwright test mux.spec.js

const { test, expect } = require('@playwright/test');

const TOKEN = process.env.BROKER_TOKEN || 'V6T4jmuDY1GDgYDmSRaFa1wwd4RTkFKv';
const MUX_URL = `/mux?token=${TOKEN}`;

// Use 'commit' (earliest response) to avoid kubectl port-forward SPDY exhaustion.
// ES module CDN imports block 'load' and sometimes 'domcontentloaded'.
const GOTO_OPTS = { waitUntil: 'commit' };

// ────────────────────────────────────────────────────────────
// 1. Page load & initial state (no WS needed)
// ────────────────────────────────────────────────────────────

test.describe('Page load', () => {
  test('serves mux HTML with correct title', async ({ page }) => {
    await page.goto(MUX_URL, GOTO_OPTS);
    await expect(page).toHaveTitle('Coop Multiplexer');
  });

  test('shows header with stats bar', async ({ page }) => {
    await page.goto(MUX_URL, GOTO_OPTS);
    const header = page.locator('#header');
    await expect(header).toBeVisible();
    await expect(header).toContainText('Coop Multiplexer');
    await expect(header).toContainText('Pods:');
    await expect(header).toContainText('Healthy:');
    await expect(header).toContainText('Alerts:');
  });

  test('shows status bar with connection indicator', async ({ page }) => {
    await page.goto(MUX_URL, GOTO_OPTS);
    const status = page.locator('#status');
    await expect(status).toBeVisible();
    await expect(page.locator('#dot')).toBeVisible();
    await expect(page.locator('#label')).toBeVisible();
  });
});

// ────────────────────────────────────────────────────────────
// 2. REST API (no browser/WS needed — uses request context)
// ────────────────────────────────────────────────────────────

test.describe('REST API', () => {
  const headers = { 'Authorization': `Bearer ${TOKEN}` };

  test('GET /api/v1/health returns 200', async ({ request }) => {
    const resp = await request.get('/api/v1/health');
    expect(resp.status()).toBe(200);
  });

  test('GET /api/v1/broker/pods returns registered pods', async ({ request }) => {
    const resp = await request.get('/api/v1/broker/pods', { headers });
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    expect(body.pods).toBeDefined();
    expect(Array.isArray(body.pods)).toBeTruthy();
    expect(body.pods.length).toBeGreaterThanOrEqual(1);

    for (const pod of body.pods) {
      expect(pod.name).toBeTruthy();
      expect(pod.coop_url).toMatch(/^http:\/\/\d+\.\d+\.\d+\.\d+:\d+$/);
      expect(typeof pod.healthy).toBe('boolean');
    }
  });

  test('GET /api/v1/broker/pods requires auth', async ({ request }) => {
    const resp = await request.get('/api/v1/broker/pods');
    expect(resp.status()).toBe(401);
  });
});

// ────────────────────────────────────────────────────────────
// 3. Auth edge cases
// ────────────────────────────────────────────────────────────

test.describe('Auth', () => {
  test('mux page loads without token', async ({ page }) => {
    await page.goto('/mux', GOTO_OPTS);
    await expect(page).toHaveTitle('Coop Multiplexer');
  });

  test('WebSocket fails without valid token (no pods appear)', async ({ page }) => {
    await page.goto('/mux?token=invalid', GOTO_OPTS);
    await page.waitForTimeout(5000);
    const tiles = page.locator('.pod-tile');
    const count = await tiles.count();
    expect(count).toBe(0);
  });
});

// ────────────────────────────────────────────────────────────
// 4. Live dashboard — single page load, multiple assertions.
//    Uses test.describe.serial + beforeAll to load once and
//    share the page across tests, minimizing port-forward load.
// ────────────────────────────────────────────────────────────

test.describe('Live dashboard', () => {
  /** @type {import('@playwright/test').Page} */
  let page;
  let wsEvents = [];

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();
    page.on('websocket', ws => {
      ws.on('framereceived', f => {
        try { wsEvents.push(JSON.parse(f.payload.toString())); } catch (_) {}
      });
    });
    await page.goto(MUX_URL, GOTO_OPTS);
    await expect(page.locator('#dot')).toHaveClass(/dot-connected/, { timeout: 30000 });
    await expect(page.locator('.pod-tile').first()).toBeVisible({ timeout: 15000 });
  });

  test.afterAll(async () => {
    await page?.close();
  });

  // -- WebSocket connection --

  test('WS connects and shows "Connected" status', async () => {
    const dot = page.locator('#dot');
    await expect(dot).toHaveClass(/dot-connected/);
    const label = page.locator('#label');
    await expect(label).toContainText('Connected');
  });

  test('WS URL includes auth token and subscriptions', async () => {
    // WS was already captured in beforeAll — verify via events
    expect(wsEvents.length).toBeGreaterThan(0);
    // The WebSocket URL was set during page load; we verify indirectly
    // by confirming we received pod data (which requires valid token+subs).
    const hasPodData = wsEvents.some(e => e.type === 'pod_online' || e.type === 'state');
    expect(hasPodData).toBeTruthy();
  });

  // -- Pod tiles --

  test('renders tiles for registered pods', async () => {
    const tiles = page.locator('.pod-tile');
    const count = await tiles.count();
    expect(count).toBeGreaterThanOrEqual(1);
  });

  test('empty state hidden once pods appear', async () => {
    const empty = page.locator('#empty');
    await expect(empty).not.toHaveClass(/visible/);
  });

  test('each tile has name, badge, expand button, and terminal', async () => {
    const firstTile = page.locator('.pod-tile').first();
    await expect(firstTile.locator('.pod-name')).toBeVisible();
    await expect(firstTile.locator('.pod-badge')).toBeVisible();
    await expect(firstTile.locator('.pod-terminal')).toBeVisible();
    await expect(firstTile.locator('.expand-btn')).toBeVisible();
  });

  test('tiles have data-pod attribute with pod name', async () => {
    const firstTile = page.locator('.pod-tile').first();
    const dataPod = await firstTile.getAttribute('data-pod');
    expect(dataPod).toBeTruthy();
    expect(dataPod.length).toBeGreaterThan(0);
  });

  test('pod names match known agents', async () => {
    const names = await page.locator('.pod-name').allTextContents();
    const known = ['gt-gastown-polecat-furiosa', 'gt-town-mayor-hq'];
    const found = names.filter(n => known.includes(n));
    expect(found.length).toBeGreaterThanOrEqual(1);
  });

  test('stat counters reflect pod count', async () => {
    const totalText = await page.locator('#stat-total').textContent();
    expect(parseInt(totalText, 10)).toBeGreaterThanOrEqual(1);
    const healthyText = await page.locator('#stat-healthy').textContent();
    expect(parseInt(healthyText, 10)).toBeGreaterThanOrEqual(1);
  });

  // -- State badges --

  test('badges show a valid state', async () => {
    const validStates = ['starting', 'working', 'idle', 'prompt', 'error', 'exited', 'offline', 'unknown'];
    const badges = page.locator('.pod-badge');
    const count = await badges.count();
    for (let i = 0; i < count; i++) {
      const text = await badges.nth(i).textContent();
      expect(validStates).toContain(text);
    }
  });

  test('badge CSS class matches badge text', async () => {
    const badges = page.locator('.pod-badge');
    const count = await badges.count();
    for (let i = 0; i < count; i++) {
      const text = await badges.nth(i).textContent();
      await expect(badges.nth(i)).toHaveClass(new RegExp(`\\b${text}\\b`));
    }
  });

  test('at least one pod has a live state (not starting)', async () => {
    const badges = page.locator('.pod-badge');
    const count = await badges.count();
    let hasLiveState = false;
    for (let i = 0; i < count; i++) {
      const text = await badges.nth(i).textContent();
      if (text !== 'starting') {
        hasLiveState = true;
        break;
      }
    }
    expect(hasLiveState).toBeTruthy();
  });

  // -- Focus management --

  test('clicking a tile focuses it', async () => {
    const firstTile = page.locator('.pod-tile').first();
    await firstTile.click();
    await expect(firstTile).toHaveClass(/focused/);
  });

  test('only one tile is focused at a time', async () => {
    const tiles = page.locator('.pod-tile');
    const count = await tiles.count();
    if (count < 2) {
      test.skip();
      return;
    }
    await tiles.first().click();
    await expect(tiles.first()).toHaveClass(/focused/);
    await tiles.nth(1).click();
    await expect(tiles.nth(1)).toHaveClass(/focused/);
    await expect(tiles.first()).not.toHaveClass(/focused/);
  });

  // -- Expand / collapse --

  test('expand button expands the tile to fullscreen', async () => {
    const firstTile = page.locator('.pod-tile').first();
    await firstTile.locator('.expand-btn').click();
    await expect(firstTile).toHaveClass(/expanded/);
    // Collapse for next test
    await firstTile.locator('.expand-btn').click();
    await expect(firstTile).not.toHaveClass(/expanded/);
  });

  test('clicking expand again collapses', async () => {
    const firstTile = page.locator('.pod-tile').first();
    const btn = firstTile.locator('.expand-btn');
    await btn.click();
    await expect(firstTile).toHaveClass(/expanded/);
    await btn.click();
    await expect(firstTile).not.toHaveClass(/expanded/);
  });

  test('Escape key collapses expanded tile', async () => {
    const firstTile = page.locator('.pod-tile').first();
    await firstTile.locator('.expand-btn').click();
    await expect(firstTile).toHaveClass(/expanded/);
    // Click header to move focus away from xterm (which swallows keydown).
    await page.locator('#header').click();
    await page.keyboard.press('Escape');
    await expect(firstTile).not.toHaveClass(/expanded/);
  });

  test('expanding a tile also focuses it', async () => {
    const tiles = page.locator('.pod-tile');
    const count = await tiles.count();
    if (count < 2) {
      test.skip();
      return;
    }
    await tiles.first().click();
    await expect(tiles.first()).toHaveClass(/focused/);
    await tiles.nth(1).locator('.expand-btn').click();
    await expect(tiles.nth(1)).toHaveClass(/expanded/);
    await expect(tiles.nth(1)).toHaveClass(/focused/);
    await expect(tiles.first()).not.toHaveClass(/focused/);
    // Collapse for cleanup
    await tiles.nth(1).locator('.expand-btn').click();
  });

  // -- Terminal rendering --

  test('xterm instances render inside pod tiles', async () => {
    const xtermEls = page.locator('.pod-terminal .xterm');
    await expect(xtermEls.first()).toBeVisible({ timeout: 5000 });
  });

  test('terminals have canvas or DOM row elements', async () => {
    const hasRendered = await page.evaluate(() => {
      const canvases = document.querySelectorAll('.pod-terminal canvas');
      for (const c of canvases) {
        if (c.width > 0 && c.height > 0) return true;
      }
      const rows = document.querySelectorAll('.pod-terminal .xterm-rows');
      for (const r of rows) {
        if (r.children.length > 0) return true;
      }
      return false;
    });
    expect(hasRendered).toBeTruthy();
  });

  // -- WebSocket events --

  test('received state events via WebSocket', async () => {
    const stateEvents = wsEvents.filter(e =>
      e.type === 'state' || e.type === 'pod_online'
    );
    expect(stateEvents.length).toBeGreaterThanOrEqual(1);
  });

  test('state events have valid pod field', async () => {
    const stateEvent = wsEvents.find(e => e.type === 'state' && e.pod);
    if (!stateEvent) {
      const onlineEvent = wsEvents.find(e => e.type === 'pod_online');
      expect(onlineEvent).toBeTruthy();
      return;
    }
    const podName = stateEvent.pod;
    const tile = page.locator(`[data-pod="${podName}"]`);
    await expect(tile).toBeVisible();
    const badge = tile.locator('.pod-badge');
    const badgeText = await badge.textContent();
    const validStates = ['starting', 'working', 'idle', 'prompt', 'error', 'exited', 'offline', 'unknown'];
    expect(validStates).toContain(badgeText);
  });
});
