import { test, expect } from './client-fixture.js';

test('Standalone Drawing uses local buyers without server warnings or lookup retries', async ({ page, request }) => {
  for (const [path, data] of [
    ['/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]],
    ['/api/tickets', [
      { prefix: 'A', t_id: 71, first_name: 'Local', last_name: 'Buyer', phone_number: '555-0171', pref: 'CALL' },
      { prefix: 'A', t_id: 72, first_name: '', last_name: '', phone_number: '', pref: 'CALL' }
    ]],
    ['/api/baskets', [{ prefix: 'A', b_id: 1, description: 'Standalone prize' }]],
    ['/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 71 }]]
  ]) {
    const response = await request.post(path, { data });
    expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
  }
  const lookups = [];
  page.on('request', (request) => {
    if (/\/api\/tickets\/A\/(71|72|73)$/.test(request.url())) lookups.push(request.url());
  });
  await page.goto('/web/drawing/A/');
  await page.locator('#id_from').fill('1');
  await page.locator('#id_to').fill('1');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  const winner = page.getByRole('status', { name: 'Basket 1 winner lookup' });
  const input = page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' });
  await expect(winner).toHaveText('Buyer, Local: 555-0171');
  await input.fill('72');
  await expect(winner).toHaveText('Ticket found; no contact info entered');
  await input.fill('73');
  await expect(winner).toHaveText('Ticket not found on this client');
  const completed = lookups.length;
  // Stay on the active page for longer than the retry interval: standalone
  // absence is a completed local lookup, not a lost server connection.
  await page.waitForTimeout(5_500);
  expect(lookups).toHaveLength(completed);
  await expect(winner).toHaveText('Ticket not found on this client');
});
