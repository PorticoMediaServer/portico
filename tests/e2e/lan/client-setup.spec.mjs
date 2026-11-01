import fs from 'node:fs';
import {test, expect} from '../web/node_modules/@playwright/test/index.mjs';

test('local-account client setup over private LAN HTTP', async ({page, context}, info) => {
  const origin = new URL(process.env.PORTICO_E2E_LAN_URL);
  await context.route('**/*', route => new URL(route.request().url()).origin === origin.origin ? route.continue() : route.abort());
  await page.goto('/');
  await page.locator('#boot').waitFor({state: 'detached', timeout: 60000});
  await page.screenshot({path: info.outputPath('01-welcome.png')});
  const local = page.getByRole('radio', {name: /local sign-in/i});
  await local.click();
  await expect(local).toBeChecked();
  await page.screenshot({path: info.outputPath('02-local-sign-in.png')});
  const setupResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/v1/setup' && r.request().method() === 'POST');
  await page.evaluate(() => {
    // Invoke the active client controller with a fresh ephemeral password. No
    // credential is put in a field, log, screenshot, trace or recording.
    let controller;
    for (const node of document.querySelectorAll('*')) {
      const key = Object.keys(node).find(k => k.startsWith('__reactFiber$'));
      for (let fiber = key && node[key], depth = 0; fiber && depth < 50; fiber = fiber.return, depth++) {
        const candidate = fiber.memoizedProps?.controller;
        if (candidate && typeof candidate.submitLocal === 'function' && typeof candidate.getSnapshot === 'function') {
          controller = candidate; break;
        }
      }
      if (controller) break;
    }
    if (!controller || controller.getSnapshot().kind !== 'form') throw new Error('Setup client controller unavailable');
    const bytes = crypto.getRandomValues(new Uint8Array(24));
    const password = Array.from(bytes, n => n.toString(16).padStart(2, '0')).join('') + 'Aa1!';
    controller.submitLocal('LAN E2E', 'e2e-owner', password, {remoteAccess: 'off', remoteSecurity: 'standard'});
  });
  const response = await setupResponse;
  expect(response.status()).toBe(201);
  const session = await response.json();
  expect(session.accessToken).toBeTruthy();
  fs.writeFileSync(process.env.PORTICO_E2E_SESSION_FILE, JSON.stringify({accessToken: session.accessToken}), {mode: 0o600});
  await expect(page.getByRole('heading', {name: 'Add your first library'})).toBeVisible({timeout: 60000});
  await page.screenshot({path: info.outputPath('03-owner-admitted.png')});
  await page.getByRole('button', {name: 'Skip for now'}).click();
  await expect(page.getByText('LAN E2E is ready')).toBeVisible({timeout: 60000});
  await expect(page.getByRole('button', {name: 'Add library', exact: true})).toBeVisible();
  await page.screenshot({path: info.outputPath('04-home.png')});
});
