import { test, expect } from './event-fixture.js';

async function post(request, client, path, data) {
  const response = await request.post(new URL(path, client.baseURL).href, { data });
  expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
}

async function openDrawing(page, client) {
  await page.goto(new URL('/web/drawing/A/', client.baseURL).href);
  await page.locator('#id_from').fill('1');
  await page.locator('#id_to').fill('1');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await expect(page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' })).toBeVisible();
}

async function waitForQueues(request, event) {
  await expect.poll(async () => Promise.all([event.a, event.b].map(async (client) => {
    const response = await request.get(new URL('/api/status', client.baseURL).href);
    const status = await response.json();
    return [status.state, !!status.recovering, status.pending, status.failed];
  })), { timeout: 20_000 }).toEqual([['connected', false, 0, 0], ['connected', false, 0, 0]]);
}

async function expectNoLocalTickets(request, client) {
  const response = await request.get(new URL('/api/backuprestore/local', client.baseURL).href);
  expect((await response.json()).tickets).toEqual([]);
}

test.beforeEach(async ({ request, event }) => {
  await post(request, event.a, '/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]);
  await post(request, event.a, '/api/baskets', [{ prefix: 'A', b_id: 1, description: 'Prize', donors: 'Donor' }]);
  await waitForQueues(request, event);
});

test('Drawing looks up another client’s buyer, distinguishes missing and blank tickets, and saves only drawing fields', async ({ page, request, event }) => {
  await post(request, event.a, '/api/tickets', [
    { prefix: 'A', t_id: 11, first_name: 'Alice', last_name: 'Buyer', phone_number: '555-0111', pref: 'CALL' },
    { prefix: 'A', t_id: 12, first_name: '', last_name: '', phone_number: '', pref: 'CALL' }
  ]);
  await post(request, event.b, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 11 }]);
  await openDrawing(page, event.b);
  const winner = page.getByRole('status', { name: 'Basket 1 winner lookup' });
  const input = page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' });
  await expect(winner).toContainText('Buyer, Alice: 555-0111');
  await expectNoLocalTickets(request, event.b);

  event.b.link.setDelay(500);
  await input.fill('999');
  await expect(winner).toContainText('Lookup pending');
  await expect(winner).toContainText('Ticket not found on server; another client may still have unsent entries');
  event.b.link.setDelay(0);
  await input.fill('12');
  await expect(winner).toContainText('Ticket found; no contact info entered');
  const sent = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/drawing');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  expect((await sent).postDataJSON()).toEqual([{ prefix: 'A', b_id: 1, winning_ticket: 12 }]);
  await waitForQueues(request, event);
  await expectNoLocalTickets(request, event.b);

  const lookups = [];
  page.on('request', (r) => { if (/\/api\/tickets\/A\/0$/.test(r.url())) lookups.push(r.url()); });
  await input.fill('0');
  await expect(winner).toBeEmpty();
  await page.waitForTimeout(100);
  expect(lookups).toEqual([]);
});

test('Drawing retries a missing buyer after another client sends its offline queue', async ({ page, request, event }) => {
  event.a.link.setOnline(false);
  await post(request, event.a, '/api/tickets', [{ prefix: 'A', t_id: 42, first_name: 'Late', last_name: 'Arrival', phone_number: '555-0142', pref: 'TEXT' }]);
  await expect.poll(async () => (await (await request.get(new URL('/api/status', event.a.baseURL).href)).json()).pending).toBe(1);
  await openDrawing(page, event.b);
  await page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' }).fill('42');
  const winner = page.getByRole('status', { name: 'Basket 1 winner lookup' });
  await expect(winner).toContainText('Ticket not found on server; another client may still have unsent entries');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  event.a.link.setOnline(true);
  await waitForQueues(request, event);
  await expect(winner).toContainText('Arrival, Late: 555-0142', { timeout: 12_000 });
  await expectNoLocalTickets(request, event.b);
  const drawing = await request.get(new URL('/api/drawing/A/1', event.b.baseURL).href);
  expect(await drawing.json()).toMatchObject({ winning_ticket: 42, first_name: 'Late' });
});

test('Drawing reports a local-only lookup and retries the shared server when its connection returns', async ({ page, request, event }) => {
  await post(request, event.a, '/api/tickets', [{ prefix: 'A', t_id: 33, first_name: 'Remote', last_name: 'Buyer', phone_number: '555-0133', pref: 'CALL' }]);
  await openDrawing(page, event.b);
  event.b.link.setOnline(false);
  await page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' }).fill('33');
  const winner = page.getByRole('status', { name: 'Basket 1 winner lookup' });
  await expect(winner).toContainText('Server lookup unavailable; this client has no local entry for this ticket');
  event.b.link.setOnline(true);
  await waitForQueues(request, event);
  await expect(winner).toContainText('Buyer, Remote: 555-0133', { timeout: 12_000 });
  await expectNoLocalTickets(request, event.b);
});
