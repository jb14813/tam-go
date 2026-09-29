import { readFile } from 'node:fs/promises';
import { test, expect } from './client-fixture.js';

test('native backup download preserves the API document and uploads through the browser', async ({ page, request }, testInfo) => {
  const ticket = { prefix: 'Backup', t_id: 42, first_name: 'R&D <Sponsor>', last_name: 'Buyer', phone_number: '555-0042', pref: 'CALL' };
  const save = await request.post('/api/tickets', { data: [ticket] });
  expect(save.ok(), await save.text()).toBeTruthy();
  const expected = await (await request.get('/api/backuprestore/local')).text();
  expect(JSON.parse(expected).revisions.length).toBeGreaterThan(0);
  await page.goto('/web/settings/backuprestore/');
  const downloading = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Local Data', exact: true }).click();
  const download = await downloading;
  const path = testInfo.outputPath('native-backup.json');
  await download.saveAs(path);
  // Conflict candidates carry hashes of their native values. Downloads must
  // preserve the API document, including escaped characters and whitespace.
  expect(await readFile(path, 'utf8')).toBe(expected);
  await page.locator('input[type=file]').setInputFiles(path);
  await page.getByRole('button', { name: 'Upload to Local', exact: true }).click();
  await expect(page.getByText('File uploaded successfully. Check to see if your data exists.')).toBeVisible();
  expect(await (await request.get('/api/tickets/Backup/42')).json()).toMatchObject(ticket);
});
