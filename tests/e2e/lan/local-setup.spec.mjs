import fs from 'node:fs';
import {test, expect} from '../web/node_modules/@playwright/test/index.mjs';

test('local-account setup over a private LAN address', async ({page, context}, info) => {
  const origin = new URL(process.env.PORTICO_E2E_LAN_URL);
  await context.route('**/*', route => new URL(route.request().url()).origin === origin.origin ? route.continue() : route.abort());
  await page.goto('/');
  await page.locator('#boot').waitFor({state: 'detached', timeout: 60000});
  await page.screenshot({path: info.outputPath('01-before-setup.png')});
  // Supply a freshly generated credential directly to the setup API from this
  // browser origin; it is never entered into a field or written to artifacts.
  const completed = await page.evaluate(async () => {
    const send = async (path, body, token) => {
      const headers = {'Content-Type': 'application/json'};
      if (token) headers.Authorization = `Bearer ${token}`;
      const response = await fetch(path, {method: 'POST', headers, body: JSON.stringify(body)});
      if (!response.ok) throw new Error(`${path}: HTTP ${response.status}`);
      return response.json();
    };
    const bytes = crypto.getRandomValues(new Uint8Array(24));
    const password = Array.from(bytes, n => n.toString(16).padStart(2, '0')).join('') + 'Aa1!';
    const bootstrap = await send('/v1/setup/browser', {});
    const uuid = crypto.getRandomValues(new Uint8Array(16));
    uuid[6] = (uuid[6] & 15) | 64; uuid[8] = (uuid[8] & 63) | 128;
    const hex = Array.from(uuid, n => n.toString(16).padStart(2, '0')).join('');
    const requestId = `${hex.slice(0,8)}-${hex.slice(8,12)}-${hex.slice(12,16)}-${hex.slice(16,20)}-${hex.slice(20)}`;
    const result = await send('/v1/setup', {requestId,
      setupToken: bootstrap.setupToken, username: 'e2e-owner', password, name: 'LAN E2E', authMode: 'local',
      interactive: true, remoteAccess: 'off', remoteSecurity: 'standard'}, bootstrap.setupToken);
    const state = await fetch('/v1/setup/state');
    return {complete: state.ok && (await state.json()).status === 'complete', accessToken: result.accessToken};
  });
  expect(completed.complete).toBe(true);
  expect(completed.accessToken).toBeTruthy();
  fs.writeFileSync(process.env.PORTICO_E2E_SESSION_FILE, JSON.stringify({accessToken: completed.accessToken}), {mode: 0o600});
  await page.reload();
  await page.locator('#boot').waitFor({state: 'detached', timeout: 60000});
  await page.screenshot({path: info.outputPath('02-after-setup.png')});
});
