import { test, expect } from './client-fixture.js';

test('settings keeps newer typing through a delayed acknowledgement and does not reload it away', async ({ page, request }) => {
  await page.goto('/web/settings/');
  await page.clock.install();
  let release;
  const barrier = new Promise((resolve) => { release = resolve; });
  let accepted;
  const reached = new Promise((resolve) => { accepted = resolve; });
  await page.route('**/api/settings', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    accepted();
    await barrier;
    await route.fulfill({ response });
  });
  await page.locator('#venue_name').fill('First accepted venue');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await reached;
  await expect(page.getByRole('button', { name: 'Pair', exact: true })).toBeDisabled();
  await page.locator('#venue_name').fill('Newer venue still being edited');
  release();
  await expect(page.getByText(/Settings saved/)).toBeVisible();
  await expect(page.locator('#venue_name')).toHaveValue('Newer venue still being edited');
  expect((await (await request.get('/api/settings')).json()).venue_name).toBe('First accepted venue');
  await page.clock.fastForward(3_500);
  await expect(page.locator('#venue_name')).toHaveValue('Newer venue still being edited');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await (await request.get('/api/settings')).json()).venue_name).toBe('Newer venue still being edited');
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
  await page.locator('#venue_name').fill('Another edit after the successful save');
  await page.clock.fastForward(3_500);
  await expect(page.locator('#venue_name')).toHaveValue('Another edit after the successful save');
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('#venue_name')).toHaveValue('Newer venue still being edited');
});
