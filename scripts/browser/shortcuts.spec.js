import { test, expect } from './client-fixture.js';

// Every Alt shortcut the forms' buttons and boxes name in their tooltips, and
// Alt+A on the main menu. The message-box test of each form is the one that
// failed in use: a message box opened by Alt+S while Alt and S were still
// held, whose key releases the page never saw, left every shortcut dead
// afterwards.

const tickets = (from, to, change = {}) => Array.from({ length: to - from + 1 }, (_, i) => ({
  prefix: 'A', t_id: from + i, first_name: `Seed${from + i}`, last_name: 'Probe',
  phone_number: '555-0100', pref: 'CALL', ...change
}));

test.beforeEach(async ({ request }) => {
  for (const [path, data] of [
    ['/api/prefixes', [{ prefix: 'A', color: 'green', weight: 1 }]],
    ['/api/tickets', tickets(1, 6)],
    ['/api/baskets', [1, 2, 3, 4, 5, 6].map((b_id) => ({
      prefix: 'A', b_id, description: `Seed basket ${b_id}`, donors: 'Donor', winning_ticket: 0
    }))],
    ['/api/drawing', [1, 2, 3, 4, 5, 6].map((b_id) => ({ prefix: 'A', b_id, winning_ticket: 0 }))]
  ]) {
    const response = await request.post(path, { data });
    expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
  }
});

const forms = [
  {
    name: 'Tickets', path: '/web/tickets/A/', pager: true,
    box: (n) => `Ticket ${n} first name`, value: 'Typed',
    read: async (request, n) => (await (await request.get(`/api/tickets/A/${n}`)).json()).first_name,
    change: (request, n, value) => request.post('/api/tickets', { data: tickets(n, n, { first_name: value }) })
  },
  {
    name: 'Baskets', path: '/web/baskets/A/', pager: true,
    box: (n) => `Basket ${n} description`, value: 'Typed basket',
    read: async (request, n) => (await (await request.get(`/api/baskets/A/${n}`)).json()).description,
    change: (request, n, value) => request.post('/api/baskets', {
      data: [{ prefix: 'A', b_id: n, description: value, donors: 'Donor', winning_ticket: 0 }]
    })
  },
  {
    name: 'Drawing', path: '/web/drawing/A/', pager: true,
    box: (n) => `Basket ${n} winning ticket`, value: '5',
    read: async (request, n) => String((await (await request.get(`/api/baskets/A/${n}`)).json()).winning_ticket),
    change: (request, n, value) => request.post('/api/drawing', {
      data: [{ prefix: 'A', b_id: n, winning_ticket: Number(value) }]
    })
  },
  {
    name: 'Search', path: '/web/search/tickets/', pager: false,
    box: (n) => `Ticket A ${n} first name`, value: 'Typed',
    read: async (request, n) => (await (await request.get(`/api/tickets/A/${n}`)).json()).first_name,
    change: (request, n, value) => request.post('/api/tickets', { data: tickets(n, n, { first_name: value }) })
  }
];

async function open(page, form) {
  await page.goto(form.path);
  if (form.pager) {
    await page.locator('#id_from').fill('1');
    await page.locator('#id_to').fill('3');
    await page.getByRole('button', { name: 'Go', exact: true }).click();
  } else {
    await page.locator('#search_last_name').fill('Probe');
    await page.locator('#search_last_name').press('Enter');
  }
  await expect(page.getByLabel(form.box(3), { exact: true })).toBeVisible();
}

const field = (page, form, n) => page.getByLabel(form.box(n), { exact: true });
// The row's Save? button: Yes when the row is marked to be saved.
const mark = (page, form, n) => page.getByRole('row')
  .filter({ has: field(page, form, n) })
  .getByRole('button', { name: /^(Yes|No)$/ });

for (const form of forms) {
  test.describe(form.name, () => {
    test('Alt+L and Alt+O move to the next and the previous row', async ({ page }) => {
      await open(page, form);
      await field(page, form, 1).click();
      await page.keyboard.press('Alt+l');
      await expect(field(page, form, 2)).toBeFocused();
      await page.keyboard.press('Alt+l');
      await expect(field(page, form, 3)).toBeFocused();
      await page.keyboard.press('Alt+o');
      await expect(field(page, form, 2)).toBeFocused();
    });

    test('Alt+J and Alt+U copy a row into the one below and the one above', async ({ page }) => {
      await open(page, form);
      await field(page, form, 2).fill(form.value);
      await page.keyboard.press('Alt+j');
      await expect(field(page, form, 3)).toHaveValue(form.value);
      await expect(mark(page, form, 3)).toHaveText('Yes');
      await field(page, form, 2).click();
      await page.keyboard.press('Alt+u');
      await expect(field(page, form, 1)).toHaveValue(form.value);
      await expect(mark(page, form, 1)).toHaveText('Yes');
    });

    test('Alt+C and Alt+V copy a row and paste it into another', async ({ page }) => {
      await open(page, form);
      await field(page, form, 1).fill(form.value);
      await page.keyboard.press('Alt+c');
      await field(page, form, 3).click();
      await page.keyboard.press('Alt+v');
      await expect(field(page, form, 3)).toHaveValue(form.value);
      await expect(mark(page, form, 3)).toHaveText('Yes');
    });

    test('Alt+V with nothing copied leaves the row as it is', async ({ page }) => {
      await open(page, form);
      await field(page, form, 2).click();
      await page.keyboard.press('Alt+v');
      await expect(mark(page, form, 2)).toHaveText('No');
    });

    test('Alt+S saves the marked rows', async ({ page, request }) => {
      await open(page, form);
      await field(page, form, 1).fill(form.value);
      await expect(mark(page, form, 1)).toHaveText('Yes');
      await page.keyboard.press('Alt+s');
      await expect(mark(page, form, 1)).toHaveText('No');
      await expect.poll(() => form.read(request, 1)).toBe(form.value);
    });

    test('Ctrl+Alt (AltGr) and Alt+Shift are not shortcuts', async ({ page, request }) => {
      await open(page, form);
      await field(page, form, 1).fill(form.value);
      await page.keyboard.press('Control+Alt+s');
      await page.keyboard.press('Alt+Shift+S');
      await expect(mark(page, form, 1)).toHaveText('Yes');
      expect(await form.read(request, 1)).not.toBe(form.value);
    });

    test('the shortcuts still work after a message box opened by Alt+S', async ({ page, request }) => {
      await open(page, form);
      // Another computer changes row 1 after this page loaded it, so the
      // save is answered with a message box.
      expect((await form.change(request, 1, form.name === 'Drawing' ? '2' : 'Other')).ok()).toBeTruthy();
      const messages = [];
      page.on('dialog', async (dialog) => {
        messages.push(dialog.message());
        // The volunteer lets go of Alt and S while the box is up, then
        // presses OK: the page never sees the keys go up.
        await page.keyboard.up('s');
        await page.keyboard.up('Alt');
        await dialog.accept();
      });
      await field(page, form, 1).fill(form === forms[2] ? '7' : 'Mine');
      await page.keyboard.down('Alt');
      await page.keyboard.down('s');
      await expect.poll(() => messages.length).toBe(1);
      expect(messages[0]).toContain('not saved');

      await field(page, form, 3).fill(form.value);
      await page.keyboard.press('Alt+c');
      await field(page, form, 2).click();
      await page.keyboard.press('Alt+v');
      await expect(field(page, form, 2)).toHaveValue(form.value);
      await page.keyboard.press('Alt+l');
      await expect(field(page, form, 3)).toBeFocused();
    });

    if (form.pager) {
      test('Alt+Q and Alt+W go to the From and To boxes', async ({ page }) => {
        await open(page, form);
        await field(page, form, 2).click();
        await page.keyboard.press('Alt+q');
        await expect(page.locator('#id_from')).toBeFocused();
        await page.keyboard.press('Alt+w');
        await expect(page.locator('#id_to')).toBeFocused();
      });

      test('Alt+N and Alt+B go to the next and the previous page', async ({ page }) => {
        await open(page, form);
        await field(page, form, 1).click();
        await page.keyboard.press('Alt+n');
        await expect(field(page, form, 4)).toBeVisible();
        await expect(field(page, form, 1)).toHaveCount(0);
        await page.keyboard.press('Alt+b');
        await expect(field(page, form, 1)).toBeVisible();
        await expect(field(page, form, 4)).toHaveCount(0);
      });
    }
  });
}

// What the browser reports for a key pressed with Alt on another layout.
const press = (page, key, code) => page.evaluate(([key, code]) => {
  (document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent('keydown', {
    key, code, altKey: true, bubbles: true, cancelable: true
  }));
}, [key, code]);

test('The shortcuts follow the letter on the key, on other layouts too', async ({ page }) => {
  const form = forms[0];
  await open(page, form);
  await field(page, form, 2).fill(form.value);
  // Dvorak: the key labelled O sits where QWERTY has S. It is Alt+O, the
  // previous line, not Alt+S.
  await press(page, 'o', 'KeyS');
  await expect(field(page, form, 1)).toBeFocused();
  await expect(mark(page, form, 2)).toHaveText('Yes');
  // Option+C on a Mac types ç: the key's place says C, the copy key.
  await field(page, form, 2).click();
  await press(page, 'ç', 'KeyC');
  await field(page, form, 3).click();
  await page.keyboard.press('Alt+v');
  await expect(field(page, form, 3)).toHaveValue(form.value);
});

test.describe('Search', () => {
  test('Alt+Q, Alt+W and Alt+E go to the first name, last name and phone boxes', async ({ page }) => {
    await page.goto('/web/search/tickets/');
    await expect(page.locator('#search_phone_number')).toBeVisible();
    await page.keyboard.press('Alt+q');
    await expect(page.locator('#search_first_name')).toBeFocused();
    await page.keyboard.press('Alt+w');
    await expect(page.locator('#search_last_name')).toBeFocused();
    await page.keyboard.press('Alt+e');
    await expect(page.locator('#search_phone_number')).toBeFocused();
  });

  test('Alt+C on the Pref button copies the row and leaves its preference', async ({ page, request }) => {
    expect((await request.post('/api/tickets', { data: tickets(1, 1, { pref: 'TEXT' }) })).ok()).toBeTruthy();
    await open(page, forms[3]);
    const pref = page.getByRole('row').filter({ has: field(page, forms[3], 1) }).getByRole('button', { name: 'TEXT' });
    await pref.focus();
    await page.keyboard.press('Alt+c');
    await expect(pref).toHaveText('TEXT');
    await expect(mark(page, forms[3], 1)).toHaveText('No');
    expect(JSON.parse(await page.evaluate(() => localStorage.getItem('tam-ticket')))).toMatchObject({ pref: 'TEXT' });
  });
});

test('Alt+A shows the admin section of the main menu and hides it again', async ({ page }) => {
  await page.goto('/web/');
  await expect(page.getByText('Mode: Standalone')).toBeVisible();
  const shutDown = page.getByRole('button', { name: 'Shut Down R-TAM', exact: true });
  await expect(shutDown).toHaveCount(0);
  await page.keyboard.press('Alt+a');
  await expect(shutDown).toBeVisible();
  await page.keyboard.press('Alt+a');
  await expect(shutDown).toHaveCount(0);
  // AZERTY: the key labelled A sits where QWERTY has Q.
  await press(page, 'a', 'KeyQ');
  await expect(shutDown).toBeVisible();
  await press(page, 'q', 'KeyA');
  await expect(shutDown).toBeVisible();
});
