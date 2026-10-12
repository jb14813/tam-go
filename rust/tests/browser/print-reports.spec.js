import { test, expect } from './event-fixture.js';

const url = (client, path) => new URL(path, client.baseURL).href;

async function post(request, client, path, data) {
  const response = await request.post(url(client, path), { data });
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

for (const report of ['bybasket', 'byname']) {
  test(`${report} print refreshes winners and preserves the offline copy notice`, async ({ page, request, event }, testInfo) => {
    await post(request, event.a, '/api/prefixes', [{ prefix: 'PRINT', color: 'green', weight: 1 }]);
    await post(request, event.a, '/api/tickets', [
      { prefix: 'PRINT', t_id: 1, first_name: 'Old', last_name: 'Winner', phone_number: '555-0101', pref: 'CALL' },
      { prefix: 'PRINT', t_id: 2, first_name: 'Fresh', last_name: 'Winner', phone_number: '555-0102', pref: 'TEXT' }
    ]);
    await post(request, event.a, '/api/baskets', [{ prefix: 'PRINT', b_id: 1, description: 'Printed prize', donors: 'Local donor', winning_ticket: 0 }]);
    await post(request, event.a, '/api/drawing', [{ prefix: 'PRINT', b_id: 1, winning_ticket: 1 }]);
    // Count the real Print action without opening a headless system dialog.
    // Report requests and values still come from the actual programs.
    await page.addInitScript(() => {
      window.printCalls = 0;
      window.print = () => { window.printCalls++; };
    });
    await page.goto(url(event.a, `/web/reports/${report}/PRINT/`));
    await expect(page.locator('tbody')).toContainText('Old');
    await post(request, event.b, '/api/drawing', [{ prefix: 'PRINT', b_id: 1, winning_ticket: 2 }]);
    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect.poll(() => page.evaluate(() => window.printCalls)).toBe(1);
    await expect(page.locator('tbody')).toContainText('Fresh');
    await expect(page.locator('tbody')).not.toContainText('Old');
    await expect(page.locator('tbody')).toContainText('555-0102');

    // A joined report response does not update the complete Go event copy.
    // Check the actual copy that printing will use after losing the link.
    const copyResponse = await request.get(url(event.a, '/api/backuprestore/local'));
    const copy = await copyResponse.json();
    const copiedWinner = copy.baskets.find(row => row.prefix === 'PRINT' && row.b_id === 1).winning_ticket;
    const copiedName = copy.tickets.find(row => row.prefix === 'PRINT' && row.t_id === copiedWinner).first_name;

    event.a.link.setOnline(false);
    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect.poll(() => page.evaluate(() => window.printCalls)).toBe(2);
    const notice = page.getByText("From this computer's copy: the server could not be reached.", { exact: false });
    await expect(notice).toBeVisible();
    await page.emulateMedia({ media: 'print' });
    await expect(page.getByRole('button', { name: 'Print', exact: true })).toBeHidden();
    await expect(page.locator('#status_bar')).toBeHidden();
    await expect(notice).toBeVisible();
    await expect(page.locator('tbody')).toContainText(copiedName);
    const path = testInfo.outputPath(`${report}-print.png`);
    await page.screenshot({ path, fullPage: true });
    await testInfo.attach(`${report} print layout`, { path, contentType: 'image/png' });
  });
}
