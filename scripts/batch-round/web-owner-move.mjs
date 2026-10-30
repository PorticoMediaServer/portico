#!/usr/bin/env node
/** Drives production UI only. Credential JSON is stdin; no tokens/journals are exported. */
import fs from 'node:fs';
import os from 'node:os';
import {pathToFileURL} from 'node:url';

if (!process.argv.includes('--execute')) {
  console.log('PLAN: genuine WEB owner login; Settings confirmed Local→Portico (keep people); Hosted signed approval; same-tab owner return. No network.');
  process.exit(0);
}
if ('CI' in process.env || 'GITHUB_ACTIONS' in process.env) throw new Error('Owner move is prohibited in CI');
if (!['ehler-work', 'ehler-homelab-mini-2'].includes(os.hostname())) throw new Error('Run browser on mini-2');
const modulePath = process.env.PORTICO_PLAYWRIGHT_MODULE;
if (!modulePath?.startsWith('/')) throw new Error('Set PORTICO_PLAYWRIGHT_MODULE to an existing qualified Playwright-core module');
const {chromium} = await import(pathToFileURL(modulePath).href);
let input = '';
for await (const part of process.stdin) {input += part; if (input.length > 64000) throw new Error('Private input too large');}
const cfg = JSON.parse(input); input = '';
for (const account of [cfg.owner, cfg.hosted]) if (typeof account?.username !== 'string' || typeof account.password !== 'string') throw new Error('Missing private credentials');
const origin = 'http://localhost:49300', hostedOrigin = 'https://web.getportico.tv';
const profile = process.env.PORTICO_OWNER_PROFILE;
if (!profile?.startsWith('/home/justin/.local/share/portico-testing/')) throw new Error('Use a protected persistent owner browser profile outside Git');
fs.mkdirSync(profile, {recursive: true, mode: 0o700});
if (fs.statSync(profile).mode & 0o077) throw new Error('Browser profile must be private');
let phase = 'browser-start';
const receipt = {passed: false, preview: false, confirmed: false, switchPrepared: false, ownerStart: false, approved: false, ownerReturn: false, failures: []};
const context = await chromium.launchPersistentContext(profile, {headless: true, executablePath: '/usr/bin/google-chrome', args: ['--no-sandbox', '--disable-dev-shm-usage'], viewport: {width: 1440, height: 900}});
const visible = locator => locator.isVisible().catch(() => false);
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
try {
  // Keep the same tab and its sessionStorage throughout the signed handoff.
  // Never export storageState or inspect the protected owner journal.
  const pages = context.pages();
  const page = pages.find(p => p.url().startsWith(hostedOrigin + '/')) ?? pages.find(p => p.url().startsWith(origin + '/')) ?? await context.newPage();
  page.setDefaultTimeout(30000);
  page.on('response', async response => {
    try {
      const url = new URL(response.url());
      if (url.origin !== origin && url.origin !== hostedOrigin) return;
      const path = url.pathname;
      if (response.status() >= 400 && receipt.failures.length < 20 && path.startsWith('/v1/')) receipt.failures.push({path, status: response.status()});
      if (url.origin !== origin || response.request().method() !== 'POST' || response.status() !== 200) return;
      if (path === '/v1/admin/sign-in/move/preview') {const body = response.request().postDataJSON(); receipt.preview = body.to === 'portico' && body.startOver === false;}
      if (path === '/v1/admin/sign-in/move/confirm') receipt.confirmed = true;
      if (path === '/v1/networking/claim/prepare') {const body = response.request().postDataJSON(); receipt.switchPrepared = body.purpose === 'switch' && !Object.hasOwn(body, 'accountId');}
      if (path === '/v1/networking/claim/web-approval') receipt.ownerStart = true;
      if (path === '/v1/auth/portico-return/exchange') receipt.ownerReturn = true;
    } catch { /* Never print a response body, URL proof or credential. */ }
  });
  if (!page.url().startsWith(hostedOrigin + '/')) {
    phase = 'local-owner-login';
    await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
    const until = Date.now() + 45000;
    while (Date.now() < until) {
      if (await visible(page.locator('input[name=username]'))) {
        await page.locator('input[name=username]').fill(cfg.owner.username);
        await page.locator('input[name=password]').fill(cfg.owner.password);
        await page.getByRole('button', {name: 'Sign in', exact: true}).click();
      }
      const primary = page.getByRole('button', {name: /Main profile/});
      if (await visible(primary)) {await primary.click(); break;}
      if (!['/', '/sign-in', '/profiles'].includes(new URL(page.url()).pathname)) break;
      await pause(1000);
    }
    phase = 'settings-move-preview';
    await page.goto(origin + '/settings?scope=server&section=signin', {waitUntil: 'domcontentloaded'});
    await page.getByRole('button', {name: 'Switch to Portico Accounts…', exact: true}).click();
    phase = 'confirm-keep-people';
    await page.getByRole('button', {name: 'Continue to web.getportico.tv', exact: true}).click();
    await page.waitForURL(url => url.origin === hostedOrigin, {timeout: 45000});
  }
  phase = 'hosted-normal-sign-in';
  let signedIn = false, continued = false;
  const deadline = Date.now() + 150000;
  while (Date.now() < deadline && !receipt.ownerReturn) {
    if (new URL(page.url()).origin === origin) {await pause(1000); continue;}
    if (new URL(page.url()).origin !== hostedOrigin) throw new Error('Unexpected authority');
    const username = page.locator('input[name=username]');
    if (!signedIn && await visible(username)) {
      await username.fill(cfg.hosted.username);
      await page.locator('input[name=password]').fill(cfg.hosted.password);
      await page.getByRole('button', {name: 'Continue', exact: true}).click(); signedIn = true;
    } else if (!receipt.approved && await visible(page.getByRole('button', {name: 'Approve', exact: true}))) {
      phase = 'signed-owner-approval';
      await page.getByRole('button', {name: 'Approve', exact: true}).click(); receipt.approved = true;
    } else if (await visible(page.getByRole('button', {name: /^Return to /}))) {
      phase = 'same-tab-owner-return';
      await page.getByRole('button', {name: /^Return to /}).click();
    } else if (!continued && await visible(page.getByRole('button', {name: 'Continue', exact: true}))) {
      await page.getByRole('button', {name: 'Continue', exact: true}).click(); continued = true;
    }
    await pause(2000);
  }
  // A resumed tab may predate this invocation's observers. It needs separate
  // authenticated identity/claim readback; never call a partial receipt passed.
  receipt.passed = receipt.preview && receipt.confirmed && receipt.switchPrepared && receipt.ownerStart && receipt.approved && receipt.ownerReturn;
  phase = receipt.passed ? 'completed-owner-return' : phase;
} catch (error) { receipt.passed = false; receipt.failureClass = ['TimeoutError', 'Error', 'TypeError'].includes(error?.name) ? error.name : 'BrowserFailure'; }
finally {await context.close();}
console.log(JSON.stringify({...receipt, phase}));
process.exitCode = receipt.passed ? 0 : 1;
