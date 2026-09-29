import { test, expect } from './client-fixture.js';

for (const kind of ['tickets', 'baskets', 'drawing']) {
  test(`${kind}: changing only From preserves the loaded sheet size`, async ({ page, request }) => {
    await request.post('/api/prefixes', { data: [{ prefix: 'PagerProbe', color: 'green', weight: 1 }] });
    await page.goto(`/web/${kind}/PagerProbe/`);
    await page.locator('#id_from').fill('1');
    await page.locator('#id_to').fill('10');
    await page.getByRole('button', { name: 'Go', exact: true }).click();
    await expect(page.locator('tbody tr')).toHaveCount(10);
    await page.locator('#id_from').fill('11');
    await page.getByRole('button', { name: 'Go', exact: true }).click();
    await expect(page.locator('tbody tr > td:first-child').first()).toHaveText('11');
    await expect(page.locator('tbody tr > td:first-child').last()).toHaveText('20');
    await expect(page.locator('#id_from')).toHaveValue('11');
    await expect(page.locator('#id_to')).toHaveValue('20');
    await expect(page.locator('tbody tr')).toHaveCount(10);
  });
}

test('prefix settings preserve newer typing through delayed save acknowledgement', async ({ page, request }) => {
  await page.goto('/web/settings/prefixes/');
  let release;
  const held = new Promise((resolve) => { release = resolve; });
  let accepted;
  const reached = new Promise((resolve) => { accepted = resolve; });
  await page.route('**/api/prefixes', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    accepted();
    await held;
    await route.fulfill({ response });
  });
  await page.locator('#form_prefix').fill('FirstSavedPrefix');
  await page.getByRole('button', { name: 'Add/Change', exact: true }).click();
  await reached;
  await page.locator('#form_prefix').fill('NewerUnsavedPrefix');
  await page.locator('#form_weight').fill('7');
  release();
  await expect(page.getByText('Prefix saved. Newer edits still need saving.', { exact: true })).toBeVisible();
  await expect(page.locator('#form_prefix')).toHaveValue('NewerUnsavedPrefix');
  await expect(page.locator('#form_weight')).toHaveValue('7');
  await expect(page.getByRole('cell', { name: 'FirstSavedPrefix', exact: true })).toBeVisible();
  expect(await (await request.get('/api/prefixes')).json()).toContainEqual({ prefix: 'FirstSavedPrefix', color: 'white', weight: 1 });
  await page.getByRole('button', { name: 'Add/Change', exact: true }).click();
  await expect(page.getByText('Prefix saved.', { exact: true })).toBeVisible();
  await expect(page.locator('#form_prefix')).toHaveValue('');
  expect(await (await request.get('/api/prefixes')).json()).toContainEqual({ prefix: 'NewerUnsavedPrefix', color: 'white', weight: 7 });
});

test('counts retain a series named Total separately from the grand total', async ({ page, request }) => {
  const before = (await (await request.get('/api/reports/counts')).json()).find((r) => r.is_total);
  const totalBuyers = before.unique_buyers + 2;
  const totalBuys = before.total_buys + 2;
  const prefixes = [{ prefix: 'Total', color: 'green', weight: 1 }, { prefix: 'CountsOther', color: 'blue', weight: 2 }];
  expect((await request.post('/api/prefixes', { data: prefixes })).ok()).toBeTruthy();
  const tickets = prefixes.map((p, index) => ({ prefix: p.prefix, t_id: 1, first_name: `CountBuyer${index}`, last_name: 'Probe', phone_number: String(index), pref: 'CALL' }));
  expect((await request.post('/api/tickets', { data: tickets })).ok()).toBeTruthy();
  const apiRows = await (await request.get('/api/reports/counts')).json();
  expect(apiRows.filter((r) => r.prefix === 'Total')).toEqual([
    expect.objectContaining({ unique_buyers: 1, total_buys: 1, is_total: false }),
    expect.objectContaining({ unique_buyers: totalBuyers, total_buys: totalBuys, is_total: true })
  ]);
  await page.goto('/web/reports/counts/');
  await expect(page.getByText('Report snapshot:', { exact: false })).toBeVisible();
  const rows = page.locator('tbody tr').filter({ has: page.getByRole('cell', { name: 'Total', exact: true }) });
  await expect(rows).toHaveCount(2);
  await expect(rows.first().getByRole('cell')).toHaveText(['Total', '1', '1']);
  await expect(rows.last().getByRole('cell')).toHaveText(['Total', String(totalBuyers), String(totalBuys)]);
});

test('unsafe numeric identities are rejected atomically and the largest supported ticket stays exact', async ({ page, request }) => {
  const prefix = 'LargeIdProbe';
  const safe = { prefix, t_id: Number.MAX_SAFE_INTEGER, first_name: 'ExactIdentity', last_name: 'Probe', phone_number: '555', pref: 'CALL' };
  expect((await request.post('/api/prefixes', { data: [{ prefix, color: 'green', weight: 1 }] })).ok()).toBeTruthy();
  expect((await request.post('/api/tickets', { data: [safe] })).ok()).toBeTruthy();
  const rejected = await request.post('/api/tickets', {
    headers: { 'Content-Type': 'application/json' },
    data: `[${JSON.stringify({ ...safe, first_name: 'MustNotSave' })},{"prefix":"${prefix}","t_id":9007199254740992,"first_name":"LowerIdentity"},{"prefix":"${prefix}","t_id":9007199254740993,"first_name":"UpperIdentity"}]`
  });
  expect(rejected.status()).toBe(400);
  expect(await rejected.text()).toMatch(/9007199254740991|safe|range/i);
  expect(await (await request.get(`/api/tickets/${prefix}/${safe.t_id}`)).json()).toMatchObject(safe);
  const native = await (await request.get('/api/backuprestore/local')).json();
  expect(native.tickets.filter((t) => t.prefix === prefix)).toEqual([safe]);
  await page.goto('/web/search/tickets/');
  await page.locator('#search_first_name').fill('ExactIdentity');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page.getByRole('cell', { name: String(safe.t_id), exact: true })).toBeVisible();
  await page.locator('tbody input').first().fill('EditedExactIdentity');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  await expect.poll(async () => (await (await request.get(`/api/tickets/${prefix}/${safe.t_id}`)).json()).first_name).toBe('EditedExactIdentity');
});

test('existing unsafe records cannot become editable rounded search results', async ({ page, request }) => {
  const safe = { prefix: 'UnsafeExistingProbe', t_id: Number.MAX_SAFE_INTEGER, first_name: 'KeepExact', last_name: '', phone_number: '', pref: 'CALL' };
  expect((await request.post('/api/tickets', { data: [safe] })).ok()).toBeTruthy();
  let writes = 0;
  await page.route('**/api/search/tickets?*', async (route) => {
    await route.fulfill({ contentType: 'application/json', body: '[{"prefix":"UnsafeExistingProbe","t_id":9007199254740993,"first_name":"UnsafeExisting","last_name":"Probe","phone_number":"555","pref":"CALL"}]' });
  });
  page.on('request', (r) => { if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/search/tickets') writes++; });
  await page.goto('/web/search/tickets/');
  await page.locator('#search_first_name').fill('UnsafeExisting');
  const refused = page.waitForEvent('dialog');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  const dialog = await refused;
  expect(dialog.message()).toContain('9007199254740991');
  await dialog.accept();
  await expect(page.locator('tbody input')).toHaveCount(0);
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  expect(writes).toBe(0);
  expect(await (await request.get(`/api/tickets/${safe.prefix}/${safe.t_id}`)).json()).toMatchObject(safe);
});

test('unsafe saved draft identities stay available but cannot be applied', async ({ page }) => {
  await page.goto('/web/search/tickets/');
  await page.evaluate(() => localStorage.setItem('tam-unsent:unsafe-test', JSON.stringify({
    page: location.pathname, savedAt: new Date().toISOString(),
    rows: [{ prefix: 'LargeIdProbe', t_id: 9007199254740992, first_name: 'UnsafeDraft', last_name: '', phone_number: '', pref: 'CALL' }]
  })));
  await page.reload();
  await page.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
  await expect(page.getByText('Invalid t_id:', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Use draft values', exact: true })).toBeDisabled();
  expect(await page.evaluate(() => localStorage.getItem('tam-unsent:unsafe-test'))).not.toBeNull();
});

test('missing native status and discovery endpoints remain visible and recover on a later poll', async ({ page }) => {
  await page.route('**/api/status', (route) => route.fulfill({ status: 404, body: 'Not Found' }));
  await page.route('**/api/servers', (route) => route.fulfill({ status: 404, body: 'Not Found' }));
  await page.goto('/web/settings/');
  await expect(page.locator('#status_bar')).toContainText('TAM client status unavailable (HTTP 404)');
  await expect(page.getByRole('alert')).toContainText('TAM server discovery unavailable (HTTP 404)');
  await expect(page.getByRole('button', { name: 'Pair', exact: true })).toBeVisible();
  await page.unroute('**/api/status');
  await page.unroute('**/api/servers');
  await expect(page.locator('#status_bar')).toHaveCount(0);
  await expect(page.getByRole('alert')).toHaveCount(0, { timeout: 7000 });
});
