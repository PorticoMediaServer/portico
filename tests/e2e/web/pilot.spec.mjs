import {test, expect} from '@playwright/test';
import path from 'node:path';

test('local server: sign in, Home and playback', async ({page, context}, info) => {
  await context.route('**/*', async route => {
    const host = new URL(route.request().url()).hostname;
    if (!['127.0.0.1', 'localhost', '[::1]'].includes(host)) return route.abort();
    return route.continue();
  });
  await page.goto('/');
  await page.locator('#boot').waitFor({state: 'detached'});
  await page.screenshot({path: info.outputPath('01-start.png')});
  console.log('STEP 01: app opened');
  // Use the real development hook and the normal client admission path. No
  // password enters the browser; tracing is off to keep session grants private.
  const ownerName = await page.evaluate(async (devModule) => {
    const {createBrowserAccountEntry} = await import('/src/platform/account-entry.ts');
    const {devE2EUsername, devE2ESignIn} = await import(devModule);
    const entry = createBrowserAccountEntry();
    const inspected = await entry.inspect(location.origin);
    if (!inspected) throw new Error('Could not inspect the test server');
    const username = await devE2EUsername(inspected.api);
    if (!username) throw new Error('Test owner hook is unavailable');
    const result = await devE2ESignIn(inspected.api, username);
    const revision = (await entry.ports.store.read()).revision;
    await entry.accept(result, inspected.api, revision, entry.operation());
    if (!['ready', 'profiles'].includes(entry.getSnapshot().phase)) throw new Error('Test owner was not admitted');
    return result.snapshot.profiles.find(profile => profile.id === result.snapshot.account.primaryProfileId)?.name;
  }, '/@fs/' + path.resolve('../../../packages/client-core/src/dev-e2e.ts'));
  if (!ownerName) throw new Error('Test owner has no primary profile');
  await page.reload();
  await page.locator('#boot').waitFor({state: 'detached', timeout: 60000});
  const home = page.locator('[data-home-document]');
  const profile = page.getByRole('button', {name: new RegExp(ownerName)});
  await expect(home.or(profile).first()).toBeVisible({timeout: 60000});
  if (await profile.isVisible()) await profile.click();
  await expect(home).toBeVisible({timeout: 60000});
  await page.screenshot({path: info.outputPath('02-home.png')});
  console.log('STEP 02: Home visible after test-owner sign-in');
  await home.getByRole('button', {name: /^(play|resume)/i}).first().click();
  const video = page.locator('video').first();
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate(element => element.currentTime), {timeout: 30_000}).toBeGreaterThan(0.5);
  await page.screenshot({path: info.outputPath('03-playback.png')});
  console.log('STEP 03: playback advanced');
});
