import {test, expect} from '@playwright/test';
import path from 'node:path';

// Each test gets an independent browser session and uses only the devtrust owner
// hook. Keep credentials and traces out of videos, screenshots and artifacts.
test.beforeEach(async ({page, context}, info) => {
  await context.route('**/*', route => {
    const host = new URL(route.request().url()).hostname;
    return ['127.0.0.1', 'localhost', '[::1]'].includes(host) ? route.continue() : route.abort();
  });
  await page.goto('/');
  await page.locator('#boot').waitFor({state: 'detached'});
  const owner = await page.evaluate(async devModule => {
    const {createBrowserAccountEntry} = await import('/src/platform/account-entry.ts');
    const {devE2EUsername, devE2ESignIn} = await import(devModule);
    const entry = createBrowserAccountEntry();
    const inspected = await entry.inspect(location.origin);
    if (!inspected) throw new Error('Could not inspect test server');
    const username = await devE2EUsername(inspected.api);
    if (!username) throw new Error('Test owner hook unavailable');
    const result = await devE2ESignIn(inspected.api, username);
    await entry.accept(result, inspected.api, (await entry.ports.store.read()).revision, entry.operation());
    if (!['ready', 'profiles'].includes(entry.getSnapshot().phase)) throw new Error('Owner admission failed');
    return result.snapshot.profiles.find(p => p.id === result.snapshot.account.primaryProfileId)?.name;
  }, '/@fs/' + path.resolve('../../../packages/client-core/src/dev-e2e.ts'));
  if (!owner) throw new Error('Test owner has no primary profile');
  await page.reload();
  await page.locator('#boot').waitFor({state: 'detached', timeout: 60_000});
  const home = page.locator('[data-home-document]');
  const profile = page.getByRole('button', {name: new RegExp(owner)});
  await expect(home.or(profile).first()).toBeVisible({timeout: 60_000});
  if (await profile.isVisible()) await profile.click();
  await expect(home).toBeVisible({timeout: 60_000});
  await capture(page, info, 'home', 'mock.html#home', 'phone-web.html#1 @1');
});

async function capture(page, info, step, desktopFrame, mobileFrame) {
  const frame = info.project.name === 'desktop' ? desktopFrame : mobileFrame;
  const screenshot = `${step}.png`;
  await page.screenshot({path: info.outputPath(screenshot)});
  // A passing interaction is not visual acceptance. A reviewer must compare
  // the relevant frame and complete the checklist from the captured evidence.
  await info.attach(`${step}-review`, {body: JSON.stringify({step, frame, screenshot,
    works: 'passed to this step', matches: 'unreviewed', referenceState: 'candidate; verify the fixture and state match before comparison',
    checklist: {layout: 'unreviewed', spacing: 'unreviewed', typography: 'unreviewed',
      color: 'unreviewed', artwork: 'unreviewed', copy: 'unreviewed', state: 'unreviewed'}}, null, 2), contentType: 'application/json'});
}

async function openSearch(page, info) {
  await page.getByRole('link', {name: 'Search', exact: true}).click();
  const search = page.getByRole('searchbox', {name: 'Search'});
  await expect(search).toBeVisible();
  await capture(page, info, 'search-empty', 'desktop-search.html#s-empty', 'phone-web.html#2 @4');
  return search;
}

test('search finds a fixture title', async ({page}, info) => {
  const search = await openSearch(page, info);
  await search.fill('Arrival');
  await expect(page.getByText('Arrival', {exact: true}).first()).toBeVisible({timeout: 60_000});
  await capture(page, info, 'search-results', 'desktop-search.html#s-top', 'phone-web.html#2 @4');
});

test('browse a library', async ({page}, info) => {
  if (info.project.name === 'desktop') await page.getByRole('link', {name: 'Movies', exact: true}).click();
  else await page.getByRole('button', {name: 'Libraries', exact: true}).click();
  await expect(page).toHaveURL(/\/librar(?:y\/|ies)/);
  if (info.project.name !== 'desktop' && /\/libraries(?:\?|$)/.test(page.url())) {
    await page.getByText('Movies', {exact: true}).first().click();
  }
  await expect(page).toHaveURL(/\/library\//);
  await expect(page.getByText('Movies', {exact: true}).first()).toBeVisible({timeout: 60_000});
  await capture(page, info, 'library', 'desktop-browse.html#grid', 'phone-web.html#1 @3');
});

test('open a title from a library', async ({page}, info) => {
  if (info.project.name === 'desktop') await page.getByRole('link', {name: 'Movies', exact: true}).click();
  else {
    await page.getByRole('button', {name: 'Libraries', exact: true}).click();
    await expect(page).toHaveURL(/\/librar(?:y\/|ies)/);
    if (/\/libraries(?:\?|$)/.test(page.url())) await page.getByText('Movies', {exact: true}).first().click();
  }
  await expect(page).toHaveURL(/\/library\//);
  const card = page.locator('article[data-card-kind] > button').first();
  await expect(card).toBeVisible({timeout: 60_000});
  await card.click();
  await expect(page.locator('[data-title-id], [data-show-id]').first()).toBeVisible({timeout: 60_000});
  await capture(page, info, 'title', 'desktop-detail.html#f1', 'phone-web.html#1 @2');
});

test('open settings from the account menu', async ({page}, info) => {
  await page.getByRole('button', {name: /account menu/i}).click();
  await page.getByRole('menuitem', {name: 'Settings', exact: true}).click();
  await expect(page).toHaveURL(/\/settings/);
  await expect(page.getByText('Playback', {exact: true}).first()).toBeVisible({timeout: 60_000});
  await capture(page, info, 'settings', 'desktop-settings.html#f1', 'phone-web.html#3 @9');
});

test('play video and open subtitle options', async ({page}, info) => {
  const home = page.locator('[data-home-document]');
  await home.getByRole('button', {name: /^(play|resume)/i}).first().click();
  const video = page.locator('video').first();
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate(element => element.currentTime), {timeout: 30_000}).toBeGreaterThan(0.5);
  await capture(page, info, 'playback', 'desktop-player.html#f1', 'phone-web.html#2 @5');
  await page.getByRole('button', {name: 'Subtitles and audio'}).first().click();
  await expect(page.getByText(/subtitles/i).first()).toBeVisible();
  await capture(page, info, 'subtitles', 'desktop-player.html#f3', 'desktop-player.html#f7 @2');
});
