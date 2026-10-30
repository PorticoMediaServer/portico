// The one real initial-setup journey, in a single tab, on the live 8443 target.
//
//   SMOKE_SETUP_ORIGIN=https://demo.getportico.tv:8443 \
//   SMOKE_CREDENTIALS=/path/to/private/credentials.json \
//   SMOKE_OUT=/path/scratch/setup-journey \
//   SMOKE_SERVER_NAME="Portico review" \
//   SMOKE_LIBRARY_PATH="/srv/portico-review-r40/Movies" \
//   node smoke/setup/journey.mjs
//
// It drives the drawn controls only: no injected storage, no API shortcut for approval, no
// auto-approval. Every step records the visible text, the controls, a screenshot, and the
// method/path/status of every /v1 and /auth request across both hosts. On the first unexpected
// state it stops and reports exactly what it saw, so the protocol owner can diagnose before any
// retry.
import {readFileSync, mkdirSync, writeFileSync, chmodSync, existsSync} from 'node:fs';
import {join} from 'node:path';
import {loadPlaywright, Evidence, settle} from '../lib/page.mjs';

const origin = (process.env.SMOKE_SETUP_ORIGIN || 'https://demo.getportico.tv:8443').replace(/\/$/, '');
const out = process.env.SMOKE_OUT || join(process.cwd(), 'setup-out');
const credsPath = process.env.SMOKE_CREDENTIALS || '';
const serverName = process.env.SMOKE_SERVER_NAME || 'Portico review';
const libraryPath = process.env.SMOKE_LIBRARY_PATH || '/srv/portico-review-r40/Movies';
mkdirSync(out, {recursive: true});

const creds = credsPath ? JSON.parse(readFileSync(credsPath, 'utf8')) : null;
const profileDir = process.env.SMOKE_PROFILE_DIR || '';

const playwright = await loadPlaywright();

// The setup flow resumes a protected draft from the browser profile, so a real run must use a
// persistent profile the app owns. It lives under 0700 private storage and is NEVER deleted by
// this script: on failure or success it stays until diagnosis closes. Nothing is exported from
// it — no storage state, no cookies — and nothing is injected into it.
let browser, context, page;
if (profileDir) {
  mkdirSync(profileDir, {recursive: true, mode: 0o700});
  try { chmodSync(profileDir, 0o700); } catch {}
  if (!existsSync(profileDir)) throw new Error(`cannot create the protected profile directory ${profileDir}`);
  context = await playwright.chromium.launchPersistentContext(profileDir, {
    executablePath: process.env.SMOKE_CHROMIUM || undefined,
    channel: process.env.SMOKE_CHROMIUM ? undefined : (process.env.SMOKE_CHANNEL || undefined),
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
    viewport: {width: 1280, height: 900},
  });
  browser = context.browser();
  page = context.pages()[0] || await context.newPage();
  console.log(`persistent profile: ${profileDir} (retained after this run)`);
} else {
  const launched = await (await import('../lib/page.mjs')).launch(playwright, {viewport: {width: 1280, height: 900}});
  browser = launched.browser; context = launched.context; page = launched.page;
  console.log('ephemeral context: a failure here cannot be resumed, as its profile is discarded');
}
const evidence = new Evidence(out);
const steps = [];
const net = [];
const timings = [];
page.on('response', response => {
  const url = new URL(response.url());
  if (!/^\/(v1|auth|setup)\b/.test(url.pathname)) return;
  // Timing evidence for the campaign record: issue and expiry, proof and return, bounded fields
  // only. Ids are reduced to a prefix; nothing from a grant or a body is kept.
  if (/^\/(v1\/setup(\/resume)?|v1\/networking\/claim(\/prepare|\/web-approval)?|v1\/server-returns|auth\/portico-return)$/.test(url.pathname)) {
    const stamp = {at: new Date().toISOString(), status: response.status(), method: response.request().method(), path: url.pathname};
    if (response.status() < 300 && response.request().method() !== 'GET') {
      response.json().then(body => {
        for (const key of ['expiresAt', 'issuedAt', 'revision', 'generation', 'state']) {
          if (body && body[key] !== undefined) stamp[key] = body[key];
        }
        const id = body && (body.setupId || body.claimId || body.registrationId || body.id);
        if (typeof id === 'string') stamp.idPrefix = id.slice(0, 6);
      }).catch(() => {});
    }
    if (response.status() >= 400) {
      response.json().then(body => {
        const code = body && body.error && typeof body.error.code === 'string' ? body.error.code.slice(0, 60) : undefined;
        if (code) stamp.errorCode = code;
      }).catch(() => {});
    }
    timings.push(stamp);
  }
  const entry = {status: response.status(), method: response.request().method(), host: url.host, path: url.pathname};
  if (response.status() >= 400) {
    // Only a bounded, known identifier: error.code. No message text, no body, no headers.
    response.json().then(body => {
      const code = body && body.error && typeof body.error.code === 'string' ? body.error.code.slice(0, 60) : undefined;
      if (code) entry.errorCode = code;
    }).catch(() => {});
  }
  net.push(entry);
});

async function snapshot(name) {
  const state = await page.evaluate(() => ({
    url: location.href,
    text: (document.body.innerText || '').replace(/\n{3,}/g, '\n\n').slice(0, 1200),
    controls: [...document.querySelectorAll('button,a[href],[role=button],input,select,textarea')].map(element => ({
      tag: element.tagName.toLowerCase(),
      text: (element.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 60),
      aria: element.getAttribute('aria-label'),
      href: element.getAttribute('href'),
      type: element.getAttribute('type'),
      value: element.tagName === 'INPUT' ? String(element.value || '').slice(0, 40) : undefined,
      disabled: element.disabled === true,
    })).slice(0, 40),
  }));
  const shot = await evidence.shot(page, name);
  steps.push({name, ...state, screenshot: shot, net: net.slice(-6)});
  console.log(`\n== ${name} :: ${state.url}`);
  console.log(state.text.split('\n').filter(Boolean).slice(0, 12).join(' | '));
  return {state, shot};
}

function controlWith(pattern) {
  return page.locator('button,a[href],[role=button]').filter({hasText: pattern}).first();
}

async function clickControl(pattern, label) {
  const target = controlWith(pattern);
  if (!(await target.count())) throw new Error(`no control matching ${label} on ${page.url()}`);
  await target.click({timeout: 10000});
  await page.waitForTimeout(2500);
}

function stepError(error, name) {
  const message = String(error && error.message ? error.message : error);
  const record = {failedAt: name, url: page.url(), message};
  writeFileSync(join(out, 'journey.json'), JSON.stringify({steps, net, timings, result: 'FAIL', ...record}, null, 2));
  console.log(`\nFAIL ${name}: ${message}`);
  return record;
}

let result = 'FAIL';
try {
  // 1. The setup entry.
  await page.goto(origin + '/setup', {waitUntil: 'domcontentloaded', timeout: 45000});
  await settle(page, {quiet: 2500});
  await snapshot('01-setup-entry');

  // 2. Server name, keep Portico Accounts selected, continue in this tab.
  const nameField = page.locator('input').first();
  if (await nameField.count()) {
    await nameField.fill(serverName, {timeout: 8000}).catch(() => {});
  }
  await snapshot('02-named');
  const setupPosts = net.length;
  await clickControl(/^Continue to Portico Account/, 'Continue to Portico Account');

  // 3. The tab must leave for Hosted. A 409 or a lingering setup screen is a failure, not a step.
  for (let attempt = 0; attempt < 30 && /:8443\/setup/.test(page.url()); attempt++) await page.waitForTimeout(1000);
  await settle(page, {quiet: 3000});
  const hosted = await snapshot('03-hosted');
  if (/:8443\/setup/.test(page.url())) {
    const calls = net.slice(setupPosts).map(call => `${call.status} ${call.method} ${call.path}`).join(', ');
    const notice = (hosted.state.text.match(/This changed while you were looking at it[^\n]*/i) || [''])[0];
    throw new Error(`setup did not leave the server: still on /setup after Continue. Requests: ${calls || 'none'}. ${notice}`);
  }

  // 4. Sign in to the established account if a form is drawn.
  if (/web\.getportico\.tv/.test(page.url())) {
    const user = page.locator('input[autocomplete=username], input[type=text]').first();
    const pass = page.locator('input[type=password]').first();
    if (await pass.count()) {
      if (!creds) throw new Error('Hosted asks for a password but no SMOKE_CREDENTIALS was given');
      if (await user.count()) await user.fill(creds.username, {timeout: 10000});
      await pass.fill(creds.password, {timeout: 10000});
      await snapshot('04-credentials-filled');
      await clickControl(/^Continue$|^Sign in$/, 'Continue');
      await page.waitForTimeout(5000);
      await settle(page, {quiet: 2000});
    }
    const afterSignIn = await snapshot('05-hosted-after-sign-in');

    // 5. Approve the server explicitly, as drawn.
    const approve = await page.locator('button,[role=button]').filter({hasText: /Approve|Connect this server|Allow/i}).first();
    if (await approve.count()) {
      await snapshot('06-before-approve');
      await approve.click({timeout: 10000});
      await page.waitForTimeout(4000);
      await settle(page, {quiet: 2500});
      await snapshot('07-after-approve');
    }
    // 6. Return performs exactly ONE protected lookup. If the managed route is not bound yet the
    // Hosted page stays approved and returns no location; that is a hold, not a failure, and this
    // run must not reload, reinitialize or retry it.
    const back = await page.locator('button,[role=button],a[href]').filter({hasText: /Return to|Back to|Go to your server|Continue to your server/i}).first();
    if (await back.count()) {
      await snapshot('08-before-return');
      await back.click({timeout: 10000});
      await page.waitForTimeout(6000);
      await settle(page, {quiet: 2500});
      if (/web\.getportico\.tv/.test(page.url())) {
        const held = await snapshot('09-return-not-ready');
        writeFileSync(join(out, 'journey.json'), JSON.stringify({steps, net, timings, result: 'RETURN-NOT-READY',
          note: 'Return performed one lookup and no location was issued; the route binding is not ready. Profile retained, nothing reloaded.',
          url: held.state.url}, null, 2));
        console.log('\nRETURN-NOT-READY: one lookup, no navigation. Holding; profile kept, nothing reloaded.');
        result = 'RETURN-NOT-READY';
        // Deliberately no navigation from here.
        await context.close();
        process.exit(3);
      }
    }
  }

  // 7. Back on the server: the owner's first-library screen.
  for (let attempt = 0; attempt < 30 && /web\.getportico\.tv/.test(page.url()); attempt++) await page.waitForTimeout(1000);
  await settle(page, {quiet: 2500});
  const returned = await snapshot('09-returned');

  // 8. First library on the drawn screen, then a real scan.
  if (/:8443/.test(page.url())) {
    const add = page.locator('button,[role=button],a[href]').filter({hasText: /Add library|Create library|Add a library|Choose folder|Browse/i}).first();
    if (await add.count()) {
      await add.click({timeout: 10000});
      await page.waitForTimeout(2500);
      await snapshot('10-add-library');
      const pathField = page.locator('input').filter({hasNot: page.locator('[type=hidden]')}).first();
      if (await pathField.count()) {
        await pathField.fill(libraryPath, {timeout: 8000}).catch(() => {});
        await snapshot('11-library-path');
        const save = await page.locator('button,[role=button]').filter({hasText: /Add|Save|Create|Continue|Scan/i}).first();
        if (await save.count()) { await save.click({timeout: 10000}); await page.waitForTimeout(4000); }
      }
      await snapshot('12-library-added');
    }
    const scan = page.locator('button,[role=button]').filter({hasText: /Scan now|Scan all|Start scan|Scan/i}).first();
    if (await scan.count()) {
      await scan.click({timeout: 10000});
      await page.waitForTimeout(5000);
      await snapshot('13-scan-started');
    }
  }

  // 9. Post-completion: reached only after a real return to the server.
  if (!/:8443/.test(page.url())) {
    throw new Error(`the tab is not on the server after the return (${page.url()})`);
  }
  await page.goto(origin + '/setup', {waitUntil: 'domcontentloaded'});
  await settle(page, {quiet: 2500});
  await snapshot('14-setup-again');
  const system = await context.request.get(origin + '/v1/system', {timeout: 20000}).then(async response => ({status: response.status(), body: await response.json()})).catch(error => ({error: String(error)}));

  const stillRequired = system.body ? system.body.setupRequired : true;
  if (stillRequired) {
    throw new Error(`setup did not complete: /v1/system still reports setupRequired=${stillRequired}`);
  }
  result = 'PASS';
  writeFileSync(join(out, 'journey.json'), JSON.stringify({steps, net, timings, system, result}, null, 2));
  console.log(`\njourney finished: ${result}; setupRequired now ${stillRequired}`);
} catch (error) {
  stepError(error, steps.length ? steps[steps.length - 1].name : 'start');
  result = 'FAIL';
} finally {
  writeFileSync(join(out, 'journey.json'), JSON.stringify({steps, net, timings, result}, null, 2));
  // A persistent profile is left intact on purpose; only ephemeral contexts are torn down.
  if (!profileDir) {
    await context.close();
    await browser.close();
  } else {
    await context.close();
  }
}
process.exit(result === 'PASS' ? 0 : 1);
