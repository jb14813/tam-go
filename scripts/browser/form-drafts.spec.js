import { test, expect } from './event-fixture.js';

async function post(request, client, path, data) {
  const response = await request.post(new URL(path, client.baseURL).href, { data });
  expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
}

async function openForm(page, client, kind = 'drawing', to = 2) {
  await page.goto(new URL(`/web/${kind}/A/`, client.baseURL).href);
  await page.locator('#id_from').fill('1');
  await page.locator('#id_to').fill(String(to));
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await expect(page.locator('tbody input').first()).toBeVisible();
}

test.beforeEach(async ({ request, event }) => {
  await post(request, event.a, '/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]);
  await post(request, event.a, '/api/baskets', [1, 2, 3].map((b_id) => ({ prefix: 'A', b_id, description: `Prize ${b_id}`, donors: 'Donor' })));
  await post(request, event.a, '/api/tickets', [1, 2, 3].map((t_id) => ({ prefix: 'A', t_id, first_name: `Buyer ${t_id}`, last_name: 'Person', phone_number: '555', pref: 'CALL' })));
  await post(request, event.b, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 11 }, { prefix: 'A', b_id: 2, winning_ticket: 22 }]);
});

test('invalid drawing row keeps all edits on page when navigating, then corrected rows save', async ({ page, request, event }) => {
  const dialogs = [];
  page.on('dialog', async (d) => { dialogs.push(d.message()); await d.accept(); });
  await openForm(page, event.b);
  await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-1');
  await page.getByLabel('Basket 2 winning ticket', { exact: true }).fill('33');
  await page.getByRole('link', { name: 'Main Menu', exact: true }).click();
  await expect.poll(() => dialogs.length).toBeGreaterThan(0);
  await expect(page).toHaveURL(/\/web\/drawing\/A\/$/);
  await expect(page.getByLabel('Basket 2 winning ticket', { exact: true })).toHaveValue('33');
  await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('44');
  await page.getByRole('link', { name: 'Main Menu', exact: true }).click();
  await expect(page).toHaveURL(/\/web\/$/);
  const rows = await (await request.get(new URL('/api/drawing/A/1/2', event.b.baseURL).href)).json();
  expect(rows.map((r) => r.winning_ticket)).toEqual([44, 33]);
});

for (const kind of ['tickets', 'baskets', 'drawing']) {
  test(`${kind}: Next Page commits its range only when loaded rows can replace the old page`, async ({ page, request, event }) => {
    await openForm(page, event.b, kind, 1);
    event.b.link.setDelay(1200);
    const path = `/api/${kind}/A/2/2`;
    const requested = page.waitForRequest((r) => r.method() === 'GET' && new URL(r.url()).pathname === path);
    const done = page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname === path);
    await page.getByRole('button', { name: 'Next Page', exact: true }).click();
    await requested;
    await page.locator('tbody input').first().fill(kind === 'drawing' ? '33' : 'Edited');
    await done;
    event.b.link.setDelay(0);
    await expect(page.locator('#id_from')).toHaveValue('1');
    await expect(page.locator('#id_to')).toHaveValue('1');
    await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(1);
    await page.getByRole('button', { name: 'Next Page', exact: true }).click();
    await expect(page.locator('#id_from')).toHaveValue('2');
    const expectedLabel = kind === 'tickets' ? 'Ticket 2 first name' : kind === 'baskets' ? 'Basket 2 description' : 'Basket 2 winning ticket';
    await expect(page.getByLabel(expectedLabel, { exact: true })).toBeVisible();
    const saved = await (await request.get(new URL(`/api/${kind}/A/1`, event.b.baseURL).href)).json();
    expect(saved[kind === 'tickets' ? 'first_name' : kind === 'baskets' ? 'description' : 'winning_ticket']).toBe(kind === 'drawing' ? 33 : 'Edited');
  });
}

test('close retains refused drafts separately for review without applying them over newer server values', async ({ page, context, request, event }) => {
  await openForm(page, event.b);
  await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-1');
  await page.getByLabel('Basket 2 winning ticket', { exact: true }).fill('33');
  await page.close({ runBeforeUnload: true });
  await expect.poll(() => page.isClosed()).toBe(true);
  await post(request, event.a, '/api/drawing', [{ prefix: 'A', b_id: 2, winning_ticket: 44 }]);
  const returned = await context.newPage();
  await openForm(returned, event.b);
  await expect(returned.getByRole('region', { name: 'Unsent edits', exact: true })).toBeVisible();
  await returned.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
  const review = returned.getByRole('region', { name: 'Unsent edits', exact: true });
  await expect(review).toContainText('-1');
  await expect(review).toContainText('33');
  await expect(review).toContainText('44');
  await expect(returned.getByLabel('Basket 2 winning ticket', { exact: true })).toHaveValue('44');
  await expect(returned.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(0);
  const saved = await (await request.get(new URL('/api/drawing/A/2', event.b.baseURL).href)).json();
  expect(saved.winning_ticket).toBe(44);
});

for (const input of ['', '1e']) {
  test(`drawing refuses ${input || 'blank'} rather than saving winner zero; explicit zero clears`, async ({ page, request, event }) => {
    page.on('dialog', (d) => d.accept());
    await openForm(page, event.b, 'drawing', 1);
    const field = page.getByLabel('Basket 1 winning ticket', { exact: true });
    await field.fill('');
    if (input) await field.pressSequentially(input);
    await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
    await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(1);
    let saved = await (await request.get(new URL('/api/drawing/A/1', event.b.baseURL).href)).json();
    expect(saved.winning_ticket).toBe(11);
    await field.fill('0');
    await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
    await expect(page.locator('tbody').getByRole('button', { name: 'No', exact: true })).toHaveCount(1);
    saved = await (await request.get(new URL('/api/drawing/A/1', event.b.baseURL).href)).json();
    expect(saved.winning_ticket).toBe(0);
  });
}

test('unavailable browser storage is visible and refused navigation keeps edits on page', async ({ page, event }) => {
  await page.addInitScript(() => {
    const set = Storage.prototype.setItem;
    Storage.prototype.setItem = function (key, value) {
      if (key.startsWith('tam-unsent:')) throw new DOMException('Quota exceeded', 'QuotaExceededError');
      return set.call(this, key, value);
    };
  });
  page.on('dialog', (d) => d.accept());
  await openForm(page, event.b);
  await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-1');
  await page.getByLabel('Basket 2 winning ticket', { exact: true }).fill('33');
  await page.getByRole('link', { name: 'Main Menu', exact: true }).click();
  await expect(page).toHaveURL(/\/web\/drawing\/A\/$/);
  await expect(page.getByRole('alert')).toContainText('could not keep a browser copy');
  await expect(page.getByLabel('Basket 2 winning ticket', { exact: true })).toHaveValue('33');
});

test('hidden-page rejection survives reload and only an explicitly reviewed draft is used', async ({ page, request, event }) => {
	await openForm(page, event.b);
	await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-1');
	await page.getByLabel('Basket 2 winning ticket', { exact: true }).fill('33');
	await page.evaluate(() => {
		Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
		document.dispatchEvent(new Event('visibilitychange'));
	});
	await page.reload();
	await post(request, event.a, '/api/drawing', [{ prefix: 'A', b_id: 2, winning_ticket: 44 }]);
	await expect(page.getByRole('region', { name: 'Unsent edits', exact: true })).toBeVisible();
	await page.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
	await expect(page.getByRole('region', { name: 'Unsent edits', exact: true })).toContainText('44');
	await page.getByRole('button', { name: 'Use draft values', exact: true }).nth(1).click();
	await expect(page.getByLabel('Basket 2 winning ticket', { exact: true })).toHaveValue('33');
	let saved = await (await request.get(new URL('/api/drawing/A/2', event.b.baseURL).href)).json();
	expect(saved.winning_ticket).toBe(44);
	await page.getByRole('button', { name: 'Save Marked', exact: true }).click();
	await expect(page.locator('tbody').getByRole('button', { name: 'No', exact: true })).toHaveCount(1);
	saved = await (await request.get(new URL('/api/drawing/A/2', event.b.baseURL).href)).json();
	expect(saved.winning_ticket).toBe(33);
	await page.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
	await expect(page.getByRole('button', { name: 'Use draft values', exact: true })).toHaveCount(1);
	await expect(page.getByRole('region', { name: 'Unsent edits', exact: true })).toContainText('-1');
});

test('two tabs keep independent refused drafts and an accepted save does not erase either', async ({ page, context, event }) => {
	await openForm(page, event.b);
	await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-1');
	await page.getByLabel('Basket 2 winning ticket', { exact: true }).fill('33');
	const second = await context.newPage();
	await openForm(second, event.b);
	await second.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-2');
	await second.getByLabel('Basket 2 winning ticket', { exact: true }).fill('55');
	await page.close({ runBeforeUnload: true });
	await second.close({ runBeforeUnload: true });
	const returned = await context.newPage();
	await openForm(returned, event.b);
	const drafts = () => returned.evaluate(() => Object.keys(localStorage).filter((key) => key.startsWith('tam-unsent:')).map((key) => JSON.parse(localStorage.getItem(key)).rows.map((r) => r.winning_ticket)).sort((a, b) => a[0] - b[0]));
	await expect.poll(drafts).toEqual([[-2, 55], [-1, 33]]);
	await returned.getByLabel('Basket 2 winning ticket', { exact: true }).fill('0');
	await returned.getByRole('button', { name: 'Save Marked', exact: true }).click();
	await expect(returned.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(0);
	await expect.poll(drafts).toEqual([[-2, 55], [-1, 33]]);
});

test('discarding one draft leaves the remaining comparison attached to its basket', async ({ page, event }) => {
	page.on('dialog', (d) => d.accept());
	await openForm(page, event.b);
	await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-1');
	await page.getByLabel('Basket 2 winning ticket', { exact: true }).fill('33');
	await page.reload();
	const review = page.getByRole('region', { name: 'Unsent edits', exact: true });
	await review.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
	await expect(review.getByRole('button', { name: 'Use draft values', exact: true }).nth(1)).toBeEnabled();
	await review.getByRole('button', { name: 'Discard this draft', exact: true }).first().click();
	await expect(review.getByRole('button', { name: 'Use draft values', exact: true })).toHaveCount(1);
	const identity = review.locator('tr').filter({ has: page.getByRole('rowheader', { name: 'Basket', exact: true }) });
	await expect(identity.locator('td')).toHaveText(['2', '2']);
	const winner = review.locator('tr').filter({ has: page.getByRole('rowheader', { name: 'Winning ticket', exact: true }) });
	await expect(winner.locator('td')).toHaveText(['33', '22']);
});

test('an older draft comparison response cannot replace a newer review', async ({ page, request, event }) => {
	await openForm(page, event.b, 'drawing', 1);
	await page.getByLabel('Basket 1 winning ticket', { exact: true }).fill('-1');
	await page.reload();
	let release;
	const held = new Promise((resolve) => { release = resolve; });
	let seen;
	const started = new Promise((resolve) => { seen = resolve; });
	let first = true;
	await page.route('**/api/drawing/A/1', async (route) => {
		if (!first) { await route.continue(); return; }
		first = false;
		const response = await route.fetch();
		seen();
		await held;
		await route.fulfill({ response });
	});
	const review = page.getByRole('region', { name: 'Unsent edits', exact: true });
	await review.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
	await started;
	await post(request, event.a, '/api/drawing', [{ prefix: 'A', b_id: 1, winning_ticket: 44 }]);
	await review.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
	const winner = review.locator('tr').filter({ has: page.getByRole('rowheader', { name: 'Winning ticket', exact: true }) }).locator('td').nth(1);
	await expect(winner).toHaveText('44');
	const oldResponse = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/drawing/A/1');
	release();
	await oldResponse;
	await expect(winner).toHaveText('44');
});

test('keyboard preference and duplicate edits survive a renderer crash as unsent drafts', async ({ page, context, event }) => {
	await openForm(page, event.b, 'tickets', 2);
	const preference = page.getByLabel('Ticket 1 contact preference', { exact: true });
	await preference.focus();
	await preference.press('t');
	await preference.press('Alt+j');
	await expect(page.getByLabel('Ticket 2 contact preference', { exact: true })).toHaveValue('TEXT');
	await expect(page.locator('tbody').getByRole('button', { name: 'Yes', exact: true })).toHaveCount(2);
	const crash = await context.newCDPSession(page);
	const crashed = page.waitForEvent('crash');
	crash.send('Page.crash').catch(() => {});
	await crashed;
	const returned = await context.newPage();
	await openForm(returned, event.b, 'tickets', 2);
	await expect(returned.getByRole('region', { name: 'Unsent edits', exact: true })).toBeVisible();
	await returned.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
	const review = returned.getByRole('region', { name: 'Unsent edits', exact: true });
	await expect(review.getByRole('button', { name: 'Use draft values', exact: true })).toHaveCount(2);
	const preferenceRows = review.locator('tr').filter({ has: returned.getByRole('rowheader', { name: 'Contact preference', exact: true }) });
	await expect(preferenceRows.nth(0).locator('td').first()).toHaveText('TEXT');
	await expect(preferenceRows.nth(1).locator('td').first()).toHaveText('TEXT');
	await expect(returned.getByLabel('Ticket 1 contact preference', { exact: true })).toHaveValue('CALL');
});

for (const kind of ['tickets', 'baskets', 'search/tickets']) {
	test(`${kind}: a refused save stops navigation and keeps a reviewable browser draft`, async ({ page, request, event }) => {
		// Chromium can send unload keepalive requests outside Playwright's
		// routing interception. Inject this one HTTP refusal at fetch so it
		// applies equally before and during reload; all reads stay real HTTP.
		await page.addInitScript((path) => {
			const fetch = window.fetch;
			window.fetch = (input, options) => {
				if (options?.method === 'POST' && new URL(input, location.href).pathname === path) {
					return Promise.resolve(new Response(JSON.stringify({ detail: 'Save refused for regression test' }), { status: 400, headers: { 'Content-Type': 'application/json' } }));
				}
				return fetch.call(window, input, options);
			};
		}, `/api/${kind}`);
		if (kind === 'search/tickets') {
			await page.goto(new URL('/web/search/tickets/', event.b.baseURL).href);
			await page.locator('#search_last_name').fill('Person');
			await page.getByRole('button', { name: 'Search', exact: true }).click();
		} else await openForm(page, event.b, kind, 1);
		await expect(page.locator('tbody input').first()).toBeVisible();
		await page.locator('tbody input').first().fill('Unsent correction');
		const formURL = page.url();
		// The input already has this value while the asynchronous save is pending.
		// Wait for its refusal and completed dialog handling before starting reload.
		const refused = page.waitForEvent('dialog').then(async (dialog) => {
			expect(dialog.type()).toBe('alert');
			expect(dialog.message()).toBe('Nothing was saved: Save refused for regression test. Your rows are still on this page.');
			await dialog.accept();
		});
		await Promise.all([
			refused,
			page.getByRole('link', { name: 'Main Menu', exact: true }).click()
		]);
		await expect(page).toHaveURL(formURL);
		await expect(page.locator('tbody input').first()).toHaveValue('Unsent correction');
		await page.reload();
		await expect(page.getByRole('region', { name: 'Unsent edits', exact: true })).toBeVisible();
		await page.getByRole('button', { name: 'Review unsent edits', exact: true }).click();
		await expect(page.getByRole('region', { name: 'Unsent edits', exact: true })).toContainText('Unsent correction');
		const path = kind === 'baskets' ? '/api/baskets/A/1' : '/api/tickets/A/1';
		const saved = await (await request.get(new URL(path, event.b.baseURL).href)).json();
		expect(saved[kind === 'baskets' ? 'description' : 'first_name']).not.toBe('Unsent correction');
	});
}
