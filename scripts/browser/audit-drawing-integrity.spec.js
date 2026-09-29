import { test, expect } from './event-fixture.js';

async function post(request, client, path, data) {
  const response = await request.post(new URL(path, client.baseURL).href, { data });
  expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
}

async function queues(request, event) {
  await expect.poll(async () => Promise.all([event.a, event.b].map(async (client) => {
    const status = await (await request.get(new URL('/api/status', client.baseURL).href)).json();
    return [status.state, !!status.recovering, status.pending, status.failed];
  })), { timeout: 20_000 }).toEqual([['connected', false, 0, 0], ['connected', false, 0, 0]]);
}

async function openDrawing(page, client) {
  await page.goto(new URL('/web/drawing/A/', client.baseURL).href);
  await page.locator('#id_from').fill('1');
  await page.locator('#id_to').fill('1');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await expect(page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' })).toBeVisible();
}

test.beforeEach(async ({ request, event }) => {
  await post(request, event.a, '/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]);
  await post(request, event.a, '/api/baskets', [{ prefix: 'A', b_id: 1, description: 'Prize', donors: 'Donor' }]);
  await post(request, event.a, '/api/tickets', [
    { prefix: 'A', t_id: 11, first_name: 'Initial', last_name: 'Buyer', phone_number: '555-0111', pref: 'CALL' },
    { prefix: 'A', t_id: 22, first_name: 'Correct', last_name: 'Winner', phone_number: '555-0222', pref: 'CALL' }
  ]);
  await queues(request, event);
});

test('audit: a successful shared lookup refreshes when another volunteer corrects the buyer', async ({ page, request, event }) => {
  await post(request, event.b, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 11 }]);
  await openDrawing(page, event.b);
  const winner = page.getByRole('status', { name: 'Basket 1 winner lookup' });
  await expect(winner).toHaveText('Buyer, Initial: 555-0111');
  await post(request, event.a, '/api/tickets', [
    { prefix: 'A', t_id: 11, first_name: 'Corrected', last_name: 'Owner', phone_number: '555-0999', pref: 'CALL' }
  ]);
  await queues(request, event);
  const actual = await request.get(new URL('/api/tickets/A/11', event.b.baseURL).href);
  expect(actual.headers()['x-tam-source']).toBe('server');
  expect(await actual.json()).toMatchObject({ first_name: 'Corrected', last_name: 'Owner', phone_number: '555-0999' });
  await expect(winner).toHaveText('Owner, Corrected: 555-0999', { timeout: 12_000 });
});

test('audit: paging does not discard a winning number typed while its previous number is saving', async ({ page, request, event }) => {
  await openDrawing(page, event.b);
  const input = page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' });
  await input.fill('11');
  await expect(page.getByRole('status', { name: 'Basket 1 winner lookup' })).toHaveText('Buyer, Initial: 555-0111');
  event.b.link.setDelay(1500);
  const sent = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/drawing');
  const loaded = page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname === '/api/drawing/A/1/1', { timeout: 4000 }).catch(() => null);
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  expect((await sent).postDataJSON()).toEqual([{ prefix: 'A', b_id: 1, winning_ticket: 11 }]);
  await input.fill('22');
  await expect(input).toHaveValue('22');
  await loaded;
  event.b.link.setDelay(0);
  await queues(request, event);
  await expect(input).toHaveValue('22');
  await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(1);
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  await expect.poll(async () => (await (await request.get(new URL('/api/drawing/A/1', event.b.baseURL).href)).json()).winning_ticket).toBe(22);
});

test('audit: overlapping saves keep a later correction marked even when it equals the first save', async ({ page, request, event }) => {
  await openDrawing(page, event.b);
  const input = page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' });
  const save = page.getByRole('button', { name: 'Save Marked', exact: true });
  const isSave = (r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/drawing';
  await input.fill('11');
  await expect(page.getByRole('status', { name: 'Basket 1 winner lookup' })).toHaveText('Buyer, Initial: 555-0111');
  event.b.link.setDelay(1500);
  const firstSent = page.waitForRequest(isSave);
  const firstDone = page.waitForResponse((r) => isSave(r.request()) && r.request().postDataJSON()[0].winning_ticket === 11);
  const secondDone = page.waitForResponse((r) => isSave(r.request()) && r.request().postDataJSON()[0].winning_ticket === 22);
  await save.click();
  await firstSent;
  await input.fill('22');
  const secondSent = page.waitForRequest(isSave);
  await save.click();
  await input.fill('11');
  await secondSent;
  await Promise.all([firstDone, secondDone]);
  event.b.link.setDelay(0);
  await queues(request, event);
  const drawing = await request.get(new URL('/api/drawing/A/1', event.b.baseURL).href);
  expect(await drawing.json()).toMatchObject({ winning_ticket: 22 });
  await expect(input).toHaveValue('11');
  await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(1);
  await save.click();
  await expect.poll(async () => (await (await request.get(new URL('/api/drawing/A/1', event.b.baseURL).href)).json()).winning_ticket).toBe(11);
});

const forms = [
  { name: 'Tickets', path: '/web/tickets/A/', label: 'Ticket 11 first name', endpoint: '/api/tickets/A/11', write: '/api/tickets', field: 'first_name', before: 'FirstEdit', after: 'LaterEdit', id: '11' },
  { name: 'Baskets', path: '/web/baskets/A/', label: 'Basket 1 description', endpoint: '/api/baskets/A/1', write: '/api/baskets', field: 'description', before: 'FirstEdit', after: 'LaterEdit', id: '1' },
  { name: 'Search', path: '/web/search/tickets/', label: 'Ticket A 11 first name', endpoint: '/api/tickets/A/11', write: '/api/search/tickets', field: 'first_name', before: 'FirstEdit', after: 'LaterEdit' }
];

for (const form of forms) {
  for (const phase of ['save', 'load']) {
    test(`audit: ${form.name} preserves an edit made during its ${phase}`, async ({ page, request, event }) => {
      await page.goto(new URL(form.path, event.b.baseURL).href);
      let load;
      if (form.name === 'Search') {
        await page.locator('#search_last_name').fill('Buyer');
        load = page.getByRole('button', { name: 'Search', exact: true });
      } else {
        await page.locator('#id_from').fill(form.id);
        await page.locator('#id_to').fill(form.id);
        load = page.getByRole('button', { name: 'Go', exact: true });
      }
      await load.click();
      const input = page.getByRole('textbox', { name: form.label, exact: true });
      await expect(input).toBeVisible();
      if (phase === 'save') await input.fill(form.before);
      event.b.link.setDelay(1000);
      const method = phase === 'save' ? 'POST' : 'GET';
      const started = page.waitForRequest((r) => r.method() === method && new URL(r.url()).pathname.startsWith(form.write));
      const completed = page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname.startsWith(form.write), { timeout: 3000 }).catch(() => null);
      await load.click();
      await started;
      await input.fill(form.after);
      await completed;
      event.b.link.setDelay(0);
      await queues(request, event);
      await expect(input).toHaveValue(form.after);
      await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(1);
      await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
      await expect.poll(async () => (await (await request.get(new URL(form.endpoint, event.b.baseURL).href)).json())[form.field]).toBe(form.after);
    });
  }
}

test('audit: a saved blank buyer refreshes and background lookups retain their last answer', async ({ page, request, event }) => {
  await post(request, event.a, '/api/tickets', [{ prefix: 'A', t_id: 33, first_name: '', last_name: '', phone_number: '', pref: 'CALL' }]);
  await post(request, event.b, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 33 }]);
  await openDrawing(page, event.b);
  const winner = page.getByRole('status', { name: 'Basket 1 winner lookup' });
  await expect(winner).toHaveText('Ticket found; no contact info entered');
  await post(request, event.a, '/api/tickets', [{ prefix: 'A', t_id: 33, first_name: 'Filled', last_name: 'Later', phone_number: '555-0033', pref: 'CALL' }]);
  await expect(winner).toHaveText('Later, Filled: 555-0033', { timeout: 12_000 });
  event.b.link.setDelay(1000);
  await page.waitForRequest((r) => new URL(r.url()).pathname === '/api/tickets/A/33', { timeout: 7000 });
  await expect(winner).toHaveText('Later, Filled: 555-0033');
});

test('audit: normal saves wait for earlier acknowledgements and keep their invocation snapshots', async ({ page, request, event }) => {
  await openDrawing(page, event.b);
  const input = page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' });
  const save = page.getByRole('button', { name: 'Save Marked', exact: true });
  const isSave = (r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/drawing';
  const events = [];
  page.on('request', (r) => { if (isSave(r)) events.push(`send ${r.postDataJSON()[0].winning_ticket}`); });
  page.on('response', (r) => { if (isSave(r.request())) events.push(`ack ${r.request().postDataJSON()[0].winning_ticket}`); });
  await input.fill('11');
  await expect(page.getByRole('status', { name: 'Basket 1 winner lookup' })).toHaveText('Buyer, Initial: 555-0111');
  event.b.link.setDelay(1000);
  const first = page.waitForRequest(isSave);
  await save.click();
  const firstRequest = await first;
  await input.fill('22');
  const second = page.waitForRequest((r) => isSave(r) && r.postDataJSON()[0].winning_ticket === 22);
  const complete = page.waitForResponse((r) => isSave(r.request()) && r.request().postDataJSON()[0].winning_ticket === 22);
  await save.click();
  await input.fill('33');
  const secondRequest = await second;
  await complete;
  event.b.link.setDelay(0);
  await queues(request, event);
  expect(events).toEqual(['send 11', 'ack 11', 'send 22', 'ack 22']);
  expect(firstRequest.headers()['x-tam-edit-session']).toMatch(/^[a-f0-9]{32}$/);
  expect(secondRequest.headers()['x-tam-edit-session']).toBe(firstRequest.headers()['x-tam-edit-session']);
  expect(firstRequest.headers()['x-tam-edit-sequence']).toBe('1');
  expect(secondRequest.headers()['x-tam-edit-sequence']).toBe('2');
  await expect(input).toHaveValue('33');
  await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(1);
});

test('audit: an immediate leave save supersedes an older queued normal save', async ({ page, request, event }) => {
  await openDrawing(page, event.b);
  const input = page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' });
  const save = page.getByRole('button', { name: 'Save Marked', exact: true });
  const isSave = (r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/drawing';
  const order = [];
  page.on('request', (r) => { if (isSave(r)) order.push(r.postDataJSON()[0].winning_ticket); });
  await input.fill('11');
  await expect(page.getByRole('status', { name: 'Basket 1 winner lookup' })).toHaveText('Buyer, Initial: 555-0111');
  event.b.link.setDelay(1000);
  const first = page.waitForRequest(isSave);
  await save.click();
  await first;
  await input.fill('22');
  const second = page.waitForRequest((r) => isSave(r) && r.postDataJSON()[0].winning_ticket === 22);
  const secondDone = page.waitForResponse((r) => isSave(r.request()) && r.request().postDataJSON()[0].winning_ticket === 22);
  await save.click();
  await input.fill('33');
  const leave = page.waitForRequest((r) => isSave(r) && r.postDataJSON()[0].winning_ticket === 33);
  const leaveDone = page.waitForResponse((r) => isSave(r.request()) && r.request().postDataJSON()[0].winning_ticket === 33);
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  const leaveRequest = await leave;
  expect(leaveRequest.headers()['x-tam-edit-sequence']).toBe('3');
  const secondRequest = await second;
  expect(secondRequest.headers()['x-tam-edit-sequence']).toBe('2');
  expect(secondRequest.headers()['x-tam-edit-session']).toBe(leaveRequest.headers()['x-tam-edit-session']);
  await Promise.all([secondDone, leaveDone]);
  event.b.link.setDelay(0);
  await queues(request, event);
  expect(order).toEqual([11, 33, 22]);
  await expect(input).toHaveValue('33');
  await expect(page.locator('tbody').getByRole('button', { name: 'No', exact: true })).toHaveCount(1);
  const drawing = await request.get(new URL('/api/drawing/A/1', event.b.baseURL).href);
  expect(await drawing.json()).toMatchObject({ winning_ticket: 33 });
});

test('audit: a save acknowledgement does not move the cursor away from a newer row edit', async ({ page, event }) => {
  await openDrawing(page, event.b);
  await page.locator('#id_to').fill('2');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  const first = page.getByRole('spinbutton', { name: 'Basket 1 winning ticket' });
  const second = page.getByRole('spinbutton', { name: 'Basket 2 winning ticket' });
  await expect(second).toBeVisible();
  await first.fill('11');
  await expect(page.getByRole('status', { name: 'Basket 1 winner lookup' })).toHaveText('Buyer, Initial: 555-0111');
  event.b.link.setDelay(1000);
  const sent = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/drawing');
  const saved = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/drawing');
  await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
  await sent;
  await second.fill('22');
  await saved;
  await expect(page.locator('tbody').getByRole('button', { name: 'No', exact: true })).toHaveCount(1);
  await expect(second).toBeFocused();
});
