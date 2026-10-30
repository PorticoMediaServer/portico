#!/usr/bin/env node
// M0 Hosted smoke: create, verify, sign in and manage a Portico Account on the live Hosted
// deployment, using Security's documented API for bootstrap and the browser for the flows.
//
//   SMOKE_HOSTED_ORIGIN=https://web.getportico.tv \
//   SMOKE_OUT=/path/to/scratch/hosted-<checkpoint> \
//   SMOKE_CHECKPOINT=v2-M0-001 \
//   SMOKE_REGISTRATION_FILE=/path/private/registration-<run>.json \
//   node smoke/hosted/run.mjs
//
// The registration id is written to SMOKE_REGISTRATION_FILE (mode 600) so Security can drop the
// real mail code into the same protected file. Nothing bypasses email verification: if no code
// arrives, the verification step is reported BLOCKED, not passed.
//
// Account creation and verification go through Playwright's context.request so the HttpOnly
// continuation cookie is the browser's own; no token is injected into page storage.
import {mkdirSync, readFileSync, writeFileSync, existsSync, chmodSync} from 'node:fs';
import {join} from 'node:path';
import {randomUUID} from 'node:crypto';
import {createReport, assert, blocked} from '../lib/report.mjs';
import {loadPlaywright, launch, Evidence, settle, clickAny, findAny, bodyText, surfaceState} from '../lib/page.mjs';

const origin = (process.env.SMOKE_HOSTED_ORIGIN || 'https://web.getportico.tv').replace(/\/$/, '');
const out = process.env.SMOKE_OUT || join(process.cwd(), 'smoke-out');
const checkpoint = process.env.SMOKE_CHECKPOINT || '';
// Required: Security's reader writes the issued code into this exact protected file.
// It replaces the file with {code, expiresAt}, so the account record lives beside it and is
// never written by anything but this runner.
const registrationFile = process.env.SMOKE_REGISTRATION_FILE || '';
const credentialsFile = process.env.SMOKE_CREDENTIALS_OUT ||
  (registrationFile ? registrationFile.replace(/registration\.json$/, 'credentials.json') : '');
const waitSeconds = Number(process.env.SMOKE_CODE_WAIT_SECONDS || 300);
const runId = new Date().toISOString().replace(/[-:tTZ.]/g, '').slice(0, 14);
const username = process.env.SMOKE_USERNAME || `smoke${runId}`.slice(0, 24);
const displayName = `Smoke ${runId.slice(-4)}`;
const password = process.env.SMOKE_PASSWORD || `Smoke-${runId}-portico`;
const reuseFile = process.env.SMOKE_REUSE_CREDENTIALS || '';
// Security asked for the reserved .invalid domain so no real mail can leave the deployment.
const contactAddress = process.env.SMOKE_CONTACT || `${username}@m0.getportico.invalid`;

mkdirSync(out, {recursive: true});
const playwright = await loadPlaywright();
const report = createReport({surface: 'hosted', origin, checkpoint, out});

function writeProtected(path, value) {
  mkdirSync(join(path, '..'), {recursive: true});
  writeFileSync(path, JSON.stringify(value, null, 2) + '\n', {mode: 0o600});
  try { chmodSync(path, 0o600); } catch {}
}

/** Waits, silently, for Security to write the real mail code into the protected file. */
async function awaitCode() {
  const deadline = Date.now() + waitSeconds * 1000;
  while (Date.now() < deadline) {
    if (existsSync(registrationFile)) {
      try {
        const record = JSON.parse(readFileSync(registrationFile, 'utf8'));
        const code = String(record.code || record.verificationCode || '').trim();
        if (code) return code;
      } catch { /* the writer may be mid-write; try again */ }
    }
    await new Promise(resolve => setTimeout(resolve, 5000));
  }
  return '';
}

async function main() {
  const {browser, context, page, events} = await launch(playwright, {viewport: {width: 1440, height: 900}});
  const evidence = new Evidence(out);
  let verified = false;
  async function signInUi(account) {
    const beforeUrl = page.url();
    const formShape = await page.evaluate(() => {
      const form = document.querySelector('form');
      const button = [...document.querySelectorAll('button')].find(b => (b.innerText || '').trim() === 'Continue');
      return {action: form ? form.getAttribute('action') : null, continueDisabled: button ? button.disabled : null};
    });
    const user = await findAny(page, [{css: 'input[autocomplete=username]'}, {css: 'input[type=text]'}], {timeout: 8000});
    const pass = await findAny(page, [{css: 'input[type=password]'}], {timeout: 8000});
    assert(user && pass, 'the sign-in form is missing a username or password field');
    await user.fill(account.username, {timeout: 10000});
    await pass.fill(account.password, {timeout: 10000});
    const mark = api.length;
    await clickAny(page, [{role: 'button', name: 'Continue'}, {role: 'button', name: 'Sign in'}, {css: 'button[type=submit]'}], {label: 'Sign in'});
    await page.waitForTimeout(4500);
    return {beforeUrl, formShape, calls: api.slice(mark)};
  }
  const readBuild = async () => {
    try {
      const response = await context.request.get(origin + '/portico-build.json', {timeout: 20000});
      return response.status() === 200 ? await response.json() : null;
    } catch { return null; }
  };
  report.buildInfo = await readBuild();
  const expectedBuild = (process.env.SMOKE_EXPECT_BUILD || '').trim();
  if (expectedBuild && (!report.buildInfo || report.buildInfo.buildId !== expectedBuild)) {
    report.note('build-pin', `STOP: expected ${expectedBuild} but the deployment serves ${report.buildInfo ? report.buildInfo.buildId : 'no build file'}`);
    await report.check('build-pin-valid', async () => {
      assert(false, 'Hosted deployment does not match the requested build pin');
    });
    report.write();
    await context.close();
    await browser.close();
    console.log('STOPPED on a build mismatch');
    process.exit(3);
  }
  // Submit evidence for Security: method, URL and status only. Never bodies or credentials.
  const network = [];
  const api = [];
  page.on('request', request => {
    if (request.method() === 'GET') return;
    const url = new URL(request.url());
    network.push({kind: 'request', method: request.method(), url: url.origin + url.pathname});
  });
  page.on('response', response => {
    const url = new URL(response.url());
    if (url.pathname.startsWith('/v1/') || url.pathname.startsWith('/v3/')) {
      network.push({kind: 'response', status: response.status(), method: response.request().method(), url: url.origin + url.pathname});
      api.push({status: response.status(), method: response.request().method(), path: url.pathname});
    }
  });

  try {
    await report.check('hosted-reachable', async () => {
      const response = await page.goto(origin + '/', {waitUntil: 'domcontentloaded', timeout: 45000});
      await settle(page, {quiet: 1500});
      const state = await surfaceState(page);
      assert(response && response.status() < 400, `Hosted answered ${response ? response.status() : 'no response'}`);
      assert(state.textLength > 40, `Hosted rendered a blank surface: ${JSON.stringify(state)}`);
      const build = report.buildInfo || {};
      return {detail: `build ${build.buildId || 'unknown'} (${build.version || 'no version'}); landed on ${new URL(state.url).pathname}`,
        evidence: {screenshot: await evidence.shot(page, 'hosted-entry'), build}};
    });

    let registrationId = '';
    let reuse = null;
    if (reuseFile) {
      try {
        reuse = JSON.parse(readFileSync(reuseFile, 'utf8'));
        writeProtected(credentialsFile || reuseFile, reuse);
      } catch (error) {
        throw new Error(`SMOKE_REUSE_CREDENTIALS is not readable: ${String(error).slice(0, 80)}`);
      }
      report.note('hosted-reuse', 'using an existing verified account; create and verify are skipped');
      await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
      await settle(page, {quiet: 2000});
      const session = await signInUi(reuse);
      const ok = session.calls.some(call => call.method === 'POST' && call.path === '/v1/sessions' && call.status === 200);
      assert(ok, `the existing account could not sign in: ${JSON.stringify(session.calls).slice(0, 200)}`);
      verified = true;
    }

    if (!reuse) await report.check('hosted-create', async () => {
      assert(registrationFile, 'SMOKE_REGISTRATION_FILE is not set; refusing to register an account nobody is reading a code for');
      const response = await context.request.post(origin + '/v1/accounts', {
        headers: {Origin: origin, 'Content-Type': 'application/json'},
        data: {username, displayName, password, contactAddress, sessionMode: 'browser', requestId: randomUUID()},
        timeout: 30000,
      });
      const body = await response.text();
      if (response.status() === 404 || response.status() === 501) {
        throw blocked(`the live deployment has no registration endpoint yet (HTTP ${response.status()}); Release's fresh schema is pending`);
      }
      assert(response.status() === 202 || response.status() === 201,
        `POST /v1/accounts answered ${response.status()}: ${body.slice(0, 300)}`);
      let json = {};
      try { json = JSON.parse(body); } catch { /* an empty body still means accepted */ }
      registrationId = json.registrationId || json.id || json.registration?.id || '';
      assert(registrationId, `the registration response carried no id: ${body.slice(0, 300)}`);
      const record = {registrationId, username, password, contactAddress, origin, createdAt: new Date().toISOString()};
      // The code file may be replaced wholesale by Security's reader; the account record may not.
      writeProtected(credentialsFile, {...record, note: 'account record, never overwritten by the code reader'});
      writeProtected(registrationFile, {...record, note: 'Security writes the real mail code here as {"code":"..."}'});
      return {detail: `registration ${registrationId.slice(0, 8)}… accepted (HTTP ${response.status()}); code file and credential record written`,
        evidence: {registrationFile, credentialsFile}};
    });

    if (!reuse) await report.check('hosted-verify', async () => {
      assert(registrationId, 'no registration was created, so there is nothing to verify');
      const status = await context.request.get(origin + '/v1/identity/current', {timeout: 20000}).then(r => r.status()).catch(() => 0);
      assert(status === 200, `the registration cookie did not carry into /v1/identity/current (HTTP ${status})`);
      const code = await awaitCode();
      if (!code) {
        throw blocked(`no verification code appeared in the protected file within ${waitSeconds}s; the real mail code has to be written there`);
      }
      const response = await context.request.post(`${origin}/v1/registrations/${registrationId}/verify`, {
        headers: {Origin: origin, 'Content-Type': 'application/json'},
        data: {code, username, displayName},
        timeout: 30000,
      });
      const body = await response.text();
      assert(response.status() === 200 || response.status() === 201,
        `verification answered ${response.status()}: ${body.slice(0, 300)}`);
      verified = true;
      return {detail: `account verified and signed in (HTTP ${response.status()})`,
        evidence: {screenshot: await evidence.shot(page, 'hosted-verified')}};
    });

    await report.check('hosted-manage', async () => {
      if (!reuse && !verified) throw blocked('no verified account, so account management is not reachable (see hosted-verify)');
      await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
      await settle(page, {quiet: 2000});
      await page.goto(origin + '/account', {waitUntil: 'domcontentloaded'});
      await settle(page, {quiet: 2500});
      const text = await bodyText(page);
      assert(/Portico Account/i.test(text), `the account screen did not render: ${JSON.stringify(text.slice(0, 160))}`);
      const tabs = ['Devices', 'Security', 'Servers', 'Notifications'].filter(name => new RegExp(name, 'i').test(text));
      assert(tabs.length >= 3, `account screen draws only ${tabs.join(', ') || 'none'} of Devices, Security, Servers and Notifications`);
      const accountReads = api.slice(-40).filter(call => call.path.startsWith('/v1/account/') && call.status === 200).map(call => call.path);
      assert(accountReads.length >= 2, `the account screen made no successful account reads: ${JSON.stringify(api.slice(-10))}`);

      // One reversible management change, with the request status as evidence.
      const notifTab = await findAny(page, [{role: 'link', name: /^Notifications/}, {role: 'button', name: /^Notifications/}], {timeout: 4000});
      let change = 'no notification tab drawn';
      if (notifTab) {
        await notifTab.click({timeout: 8000});
        await settle(page, {quiet: 1500});
        const toggle = await findAny(page, [{role: 'switch'}, {css: '[aria-checked]'}, {css: 'input[type=checkbox]'}], {timeout: 4000});
        if (toggle) {
          const before = await toggle.getAttribute('aria-checked').catch(() => null);
          const mark = api.length;
          await toggle.click({timeout: 8000});
          await page.waitForTimeout(2000);
          const calls = api.slice(mark).filter(call => call.method !== 'GET' || call.path.includes('notification'));
          const wrote = calls.find(call => call.method !== 'GET');
          const after = await toggle.getAttribute('aria-checked').catch(() => null);
          change = `${before} -> ${after}; ${wrote ? `${wrote.method} ${wrote.path} ${wrote.status}` : 'no write request seen'}`;
          // Put it back.
          await toggle.click({timeout: 8000}).catch(() => {});
          await page.waitForTimeout(1200);
        } else {
          change = 'notification preferences drew no toggle';
        }
      }
      return {detail: `tabs ${tabs.join(', ')}; reads ${accountReads.slice(0, 4).join(', ')}; notification change: ${change}`,
        evidence: {screenshot: await evidence.shot(page, 'hosted-account'), tabs, accountReads: accountReads.slice(0, 8), change}};
    });

    await report.check('hosted-signin', async () => {
      if (!reuse && !verified) throw blocked('no verified account to sign in with (see hosted-verify)');
      // A fresh cookie jar is a cleaner proof than clearing cookies under a live session,
      // which makes the app try to revoke a token it no longer has.
      const fresh = await context.browser().newContext({viewport: {width: 1440, height: 900}});
      const calls = [];
      try {
        const freshPage = await fresh.newPage();
        freshPage.on('request', request => {
          if (request.method() !== 'GET') calls.push({kind: 'request', method: request.method(), path: new URL(request.url()).pathname});
        });
        freshPage.on('response', response => {
          const url = new URL(response.url());
          if (url.pathname.startsWith('/v1/')) calls.push({kind: 'response', status: response.status(), method: response.request().method(), path: url.pathname});
        });
        await freshPage.goto(origin + '/', {waitUntil: 'domcontentloaded', timeout: 45000});
        await freshPage.waitForTimeout(2500);
        const account = reuse || {username, password};
        await freshPage.locator('input[autocomplete=username]').first().fill(account.username, {timeout: 10000});
        await freshPage.locator('input[type=password]').first().fill(account.password, {timeout: 10000});
        const formShape = await freshPage.evaluate(() => {
          const form = document.querySelector('form');
          const button = [...document.querySelectorAll('button')].find(b => (b.innerText || '').trim() === 'Continue');
          return {action: form ? form.getAttribute('action') : null, continueDisabled: button ? button.disabled : null};
        });
        await freshPage.getByRole('button', {name: 'Continue', exact: true}).first().click({timeout: 10000});
        await freshPage.waitForTimeout(5000);
        const state = await surfaceState(freshPage);
        const text = await bodyText(freshPage);
        const sessionCall = calls.find(call => call.kind === 'response' && call.method === 'POST' && call.path === '/v1/sessions');
        const signedInSurface = /Portico Account is ready|Open your account|Sign out|Home|Welcome to Portico/i.test(text);
        assert(sessionCall && sessionCall.status >= 200 && sessionCall.status < 300,
          `sign-in did not post a successful session: ${JSON.stringify(calls).slice(0, 300)}`);
        assert(signedInSurface, `sign-in answered ${sessionCall.status} but no signed-in surface rendered: ${text.replace(/\n/g, ' ').slice(0, 200)}`);
        assert(!/\/v3\/signin/.test(state.url), `sign-in bounced to the identity provider at ${new URL(state.url).pathname}`);
        return {detail: `POST /v1/sessions ${sessionCall.status}; landed ${new URL(state.url).pathname}; form action ${formShape.action || 'script-handled'}; Continue disabled=${formShape.continueDisabled}`,
          evidence: {screenshot: await evidence.shot(freshPage, 'hosted-signed-in'), calls: calls.slice(0, 14), formShape}};
      } finally {
        await fresh.close();
      }
    });

    await report.blocked('hosted-server-link',
      'BLOCKED-ON Security: complete initial Hosted server setup/link and protected journal are not implemented on main');
  } finally {
    await context.close();
    await browser.close();
  }

  const result = report.write();
  console.log(`\n${result.verdict.toUpperCase()} ${result.counts.pass} pass / ${result.counts.fail} fail / ${result.counts.blocked} blocked`);
  console.log(`page errors ${events.pageErrors.length}, console errors ${events.consoleErrors.length}`);
  process.exit(result.verdict === 'fail' ? 1 : 0);
}

main().catch(error => {
  console.error('hosted smoke crashed: ' + String(error && error.stack ? error.stack : error).slice(0, 1500));
  process.exit(2);
});
