import { test, expect } from './event-fixture.js';

async function post(request, client, path, data) {
  const response = await request.post(new URL(path, client.baseURL).href, { data });
  expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
}

test.beforeEach(async ({ request, event }) => {
  await post(request, event.a, '/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]);
  await post(request, event.a, '/api/tickets', [{ prefix: 'A', t_id: 42, first_name: 'Shared', last_name: 'Buyer', phone_number: '555-0042', pref: 'CALL' }]);
  await post(request, event.b, '/api/baskets', [{ prefix: 'A', b_id: 1, description: 'Prize' }]);
  await post(request, event.b, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 42 }]);
});

for (const path of ['byname/A', 'bybasket/A', 'counts']) {
  test(`reports: ${path} explains an outage instead of displaying a partial local report`, async ({ page, event }) => {
    event.a.link.setOnline(false);
    await page.goto(new URL(`/web/reports/${path}/`, event.a.baseURL).href);
    await expect(page.getByText(/reports are unavailable/i)).toBeVisible();
    await expect(page.locator('table')).toHaveCount(0);
    await page.emulateMedia({ media: 'print' });
    await expect(page.getByText(/reports are unavailable/i)).toBeVisible();
  });
}

for (const kind of ['byname', 'bybasket']) {
  test(`reports: ${kind} Print refreshes corrected winners and refuses an unavailable shared report`, async ({ page, request, event }) => {
    await page.goto(new URL(`/web/reports/${kind}/A/`, event.a.baseURL).href);
    await expect(page.getByRole('cell', { name: 'Buyer, Shared', exact: true })).toBeVisible();
    await post(request, event.a, '/api/tickets', [{ prefix: 'A', t_id: 43, first_name: 'Corrected', last_name: 'Winner', phone_number: '555-0043', pref: 'CALL' }]);
    await post(request, event.b, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 43 }]);
    await page.evaluate(() => { window.prints = []; window.print = () => window.prints.push(document.body.innerText); });
    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect.poll(() => page.evaluate(() => window.prints.length)).toBe(1);
    const printed = await page.evaluate(() => window.prints[0]);
    expect(printed).toContain('Winner, Corrected');
    expect(printed).not.toContain('Buyer, Shared');
    expect(printed).toContain('Report snapshot:');
    event.a.link.setOnline(false);
    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('reports are unavailable');
    expect(await page.evaluate(() => window.prints.length)).toBe(1);
    await expect(page.locator('tbody tr')).toHaveCount(0);
    await page.emulateMedia({ media: 'print' });
    await expect(page.getByRole('alert')).toBeVisible();
  });
}

test('reports: counts clears stale figures on failure and retries the selected interval', async ({ page, event }) => {
  await page.goto(new URL('/web/reports/counts/', event.a.baseURL).href);
  await expect(page.locator('tbody tr').filter({ has: page.getByRole('cell', { name: 'A', exact: true }) })).toHaveCount(1);
  await expect(page.locator('tbody tr').filter({ has: page.getByRole('cell', { name: 'A', exact: true }) })).toContainText('1');
  await page.clock.install();
  await page.locator('#interval_select').selectOption('30000');
  await page.route('**/api/reports/counts', (route) => route.fulfill({ status: 503, json: { detail: 'Event reports are unavailable until this client reconnects.' } }));
  await page.getByRole('button', { name: /^Refresh/ }).click();
  await expect(page.getByRole('alert')).toContainText('reports are unavailable');
  await expect(page.locator('table')).toHaveCount(0);
  await page.emulateMedia({ media: 'print' });
  await expect(page.getByRole('alert')).toBeVisible();
  await page.unroute('**/api/reports/counts');
  await page.clock.fastForward(30_001);
  await expect(page.locator('tbody tr').filter({ has: page.getByRole('cell', { name: 'A', exact: true }) })).toHaveCount(1);
  await expect(page.getByRole('alert')).toHaveCount(0);
});

test('reports: a delayed older counts response cannot hide the latest failure', async ({ page, event }) => {
  await page.goto(new URL('/web/reports/counts/', event.a.baseURL).href);
  await expect(page.locator('tbody tr').filter({ has: page.getByRole('cell', { name: 'A', exact: true }) })).toHaveCount(1);
  let release;
  const barrier = new Promise((resolve) => { release = resolve; });
  let requests = 0;
  await page.route('**/api/reports/counts', async (route) => {
    if (++requests === 1) {
      await barrier;
      await route.fulfill({ status: 200, json: [{ prefix: 'A', unique_buyers: 99, total_buys: 99 }] });
    } else {
      await route.fulfill({ status: 503, json: { detail: 'Event reports are unavailable until this client reconnects.' } });
    }
  });
  await page.getByRole('button', { name: /^Refresh/ }).click();
  await expect.poll(() => requests).toBe(1);
  await page.getByRole('button', { name: /^Refresh/ }).click();
  await expect(page.getByRole('alert')).toContainText('reports are unavailable');
  const oldResponse = page.waitForResponse((response) => response.url().endsWith('/api/reports/counts') && response.status() === 200);
  release();
  await oldResponse;
  await expect(page.locator('table')).toHaveCount(0);
  await expect(page.getByRole('alert')).toContainText('reports are unavailable');
});
