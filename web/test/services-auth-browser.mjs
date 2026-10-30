// Actual isolated Hosted signup and delivered-code proof. The runtime owner writes
// only the matching throwaway code into the active job's private channel.
import assert from 'node:assert/strict';
import {chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import path from 'node:path';
import {runtimeTestPassword} from './services-test-password.mjs';

export async function runServicesAuthProof({driver, port, capture, report}) {
  assert.equal(port, 19495);
  report.qualification = 'Actual isolated Hosted signup, genuine delivered code, canonical cookie session, account reload and targeted sign-out. Development IP-loopback cookies only. Production mail links, external providers, Server return and UI acceptance are not claimed.';
  const recipient = `services-r36${Date.now()}@example.test`;
  const channel = path.join(process.env.TMPDIR, 'mail-code');
  mkdirSync(channel, {mode: 0o700}); chmodSync(channel, 0o700);
  const codeFile = path.join(channel, 'code');
  writeFileSync(codeFile, '', {mode: 0o600});
  let family;
  const identity = () => driver.evaluate("import('/src/services/session.ts').then(({servicesAccount})=>{const s=servicesAccount().service.getSnapshot().session;return s?{accountId:s.account.id,familyId:s.familyId}:null})");
  try {
    await driver.viewport(1440, 874);
    await driver.navigate(`http://127.0.0.1:${port}/`);
    await driver.waitFor("document.querySelector('input[name=username]')", 45000);
    await driver.evaluate('document.fonts.ready'); await capture('hosted-signin');
    await driver.clickText('Create account');
    await driver.waitFor("document.querySelector('input[name=email]')");
    await driver.fill('input[name=email]', recipient);
    await driver.fill('input[name=username]', 'harbourview' + Date.now().toString().slice(-5));
    await driver.fill('input[name=name]', 'Alex');
    await driver.fill('input[name=new-password]', runtimeTestPassword());
    await driver.evaluate('document.activeElement.blur()'); await capture('hosted-create');
    await driver.clickText('Continue');
    await driver.waitFor("document.querySelector('input[autocomplete=one-time-code]')", 30000);
    const registrationId = await driver.evaluate("import('/src/services/session.ts').then(async({servicesAccount})=>(await servicesAccount().client.registration()).registrationId)");
    assert.equal(typeof registrationId, 'string');
    await driver.evaluate('document.fonts.ready'); await capture('hosted-code');
    console.log(JSON.stringify({event: 'AUTH_CODE_REQUIRED', recipient, registrationId, codeDestination: codeFile}));
    let code = '';
    const deadline = Date.now() + 6 * 60 * 1000;
    while (Date.now() < deadline) {
      if (existsSync(codeFile)) code = readFileSync(codeFile, 'utf8').trim();
      if (/^[0-9]{6}$/.test(code)) break;
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    assert.match(code, /^[0-9]{6}$/, 'The sole runtime owner must supply the actual matching code privately.');
    await driver.fill('input[autocomplete=one-time-code]', code);
    code = ''; rmSync(codeFile, {force: true});
    await driver.clickText('Create account');
    await driver.waitFor("document.body.innerText.includes('No servers yet')", 30000);
    family = await identity(); assert(family?.familyId && family.accountId);
    report.family = family;
    const cookies = (await driver.send('Network.getAllCookies')).cookies;
    const refresh = cookies.find(cookie => cookie.name === 'portico_refresh_dev');
    assert(refresh?.httpOnly && refresh.sameSite === 'Lax' && !refresh.secure);
    report.cookie = {httpOnly: true, sameSite: 'Lax', secure: false, qualification: 'IP-loopback development cookie only'};
    await capture('hosted-no-server');
    await driver.navigate(`http://127.0.0.1:${port}/account`);
    await driver.waitFor("document.querySelector('h1')?.textContent==='Portico Account'", 30000);
    assert.deepEqual(await identity(), family);
    await capture('hosted-account');
    await driver.clickText('Sign out');
    await driver.waitFor("document.querySelector('input[name=username]')", 20000);
    assert.equal(await identity(), null);
    const ended = await driver.evaluate("import('/src/services/session.ts').then(async({servicesAccount})=>{const a=servicesAccount();await a.service.retryRevocations();const s=a.service.getSnapshot();return {signedOut:!s.session,pendingRevocations:s.pendingRevocations}})");
    assert(ended.signedOut && ended.pendingRevocations === 0, 'Do not close the browser before canonical revocation finishes.');
    report.targetedSignOut = ended;
    report.checks = {genuineSignup: true, deliveredCodeEntry: true, sameFamilyAfterDocumentReload: true, targetedSignOut: true,
      noServerBootstrap: ![...driver.requests.values()].some(url => /\/api\/system|\/api\/servers/.test(new URL(url).pathname))};
  } finally {
    rmSync(channel, {recursive: true, force: true});
    // Retire only this test-created current family even after an assertion failure.
    const current = await identity().catch(() => null);
    if (current?.familyId) {
      report.family ??= current;
      assert(!family || current.familyId === family.familyId, 'Cleanup must never replace a different account family.');
      report.cleanup = await driver.evaluate("import('/src/services/session.ts').then(async({servicesAccount})=>{const a=servicesAccount();await a.service.logout();return {signedOut:!a.service.getSnapshot().session}})").catch(() => ({signedOut: false}));
    }
  }
}
