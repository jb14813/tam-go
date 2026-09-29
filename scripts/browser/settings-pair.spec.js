import { test, expect } from './event-fixture.js';

test('pairing keeps unrelated settings edits made while its acknowledgement is delayed', async ({ page, request, event }) => {
  await page.goto(new URL('/web/settings/', event.b.baseURL).href);
  let release;
  const barrier = new Promise((resolve) => { release = resolve; });
  let accepted;
  const reached = new Promise((resolve) => { accepted = resolve; });
  await page.route('**/api/pair', async (route) => {
    const response = await route.fetch();
    accepted();
    await barrier;
    await route.fulfill({ response });
  });
  await page.locator('#pair_password').fill('browser-event-password');
  await page.getByRole('button', { name: 'Pair again', exact: true }).click();
  await reached;
  await page.locator('#venue_name').fill('Keep this newer venue edit');
  release();
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
  await expect(page.locator('#venue_name')).toHaveValue('Keep this newer venue edit');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await (await request.get(new URL('/api/settings', event.b.baseURL).href)).json()).venue_name).toBe('Keep this newer venue edit');
});
