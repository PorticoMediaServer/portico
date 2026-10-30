// Disposable, account-free presentation/interaction proof. Real API/mail acceptance is separate.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {ChromeDriver} from './integration/chrome-driver.mjs';
import {runtimeTestPassword} from './services-test-password.mjs';

const repository = fileURLToPath(new URL('../../', import.meta.url));
const scratch = process.env.TMPDIR;
const output = process.env.PORTICO_SERVICES_OUTPUT;
const browser = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ?? process.env.PORTICO_CHROME_BIN ?? '/usr/bin/google-chrome';
assert(scratch && path.isAbsolute(scratch), 'TMPDIR must name the owned on-disk job scratch.');
assert(output && path.isAbsolute(output), 'PORTICO_SERVICES_OUTPUT must name the owned artifact directory.');
assert(existsSync(browser), 'An installed system Chrome is required; this test never downloads one.');
mkdirSync(scratch, {recursive: true}); mkdirSync(output, {recursive: true});
const temporary = mkdtempSync(path.join(scratch, 'services-links-browser-'));
const freePort = async () => {
  const socket = net.createServer(); await new Promise(resolve => socket.listen(0, '127.0.0.1', resolve));
  const port = socket.address().port; await new Promise(resolve => socket.close(resolve)); return port;
};
const rss = pid => {
  try {
    const own = Number(readFileSync(`/proc/${pid}/status`, 'utf8').match(/^VmRSS:\s+(\d+)/m)?.[1] ?? 0);
    return own + readFileSync(`/proc/${pid}/task/${pid}/children`, 'utf8').trim().split(/\s+/).filter(Boolean).reduce((sum, child) => sum + rss(child), 0);
  } catch { return 0; }
};
const stop = async child => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  await new Promise(resolve => {
    const timer = setTimeout(resolve, 3000); child.once('exit', () => { clearTimeout(timer); resolve(); });
  });
  if (child.exitCode === null && child.signalCode === null) {
    child.kill('SIGKILL'); await new Promise(resolve => child.once('exit', resolve));
  }
};
let vite, chrome, driver, peakRSSKiB = 0;
const timer = setInterval(() => { peakRSSKiB = Math.max(peakRSSKiB, rss(process.pid)); }, 200);
const report = {qualification: 'Real reset controller with an unsubmitted synthetic token and canonical password strength. Completed views are the same stateless views used by the controllers. No real reset, verification, mail, account session, Server or physical-device acceptance.', checks: {}, states: []};
if (process.env.PORTICO_SERVICES_SCENARIO === 'device') report.qualification = 'Controlled actual DeviceView/DeviceDecision presentation with exact account.html2/3/5/6 values. No API interception, session, device grant, approval, Server or physical-device acceptance.';
try {
  const port = process.env.PORTICO_SERVICES_SCENARIO === 'auth' ? 19495 : await freePort(), debugPort = await freePort();
  const config = path.join(temporary, 'vite.config.mjs');
  writeFileSync(config, `import base from ${JSON.stringify(path.join(repository, 'web/vite.config.ts'))};
export default async env => {const config=await(typeof base==='function'?base(env):base);return {...config, root:${JSON.stringify(path.join(repository, 'web'))}, cacheDir:${JSON.stringify(path.join(temporary, 'vite-cache'))},server:{...config.server,host:'127.0.0.1',port:${port},strictPort:true}}};\n`);
  const viteLog = openSync(path.join(output, 'services-vite.log'), 'w', 0o600);
  vite = spawn(process.execPath, [path.join(repository, 'node_modules/vite/bin/vite.js'), '--config', config], {cwd: repository, env: process.env, stdio: ['ignore', viteLog, viteLog]});
  for (let attempt = 0; attempt < 60; attempt++) {
    if (vite.exitCode !== null) throw new Error('The owned fixture Vite exited before readiness.');
    try { if ((await fetch(`http://127.0.0.1:${port}/test/services-links.html`)).ok) break; } catch {}
    if (attempt === 59) throw new Error('The owned fixture Vite did not become ready.');
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  const browserLog = openSync(path.join(output, 'services-chrome.log'), 'w', 0o600);
  chrome = spawn(browser, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--disable-default-apps', '--no-first-run', '--no-default-browser-check', '--renderer-process-limit=1', '--num-raster-threads=1', '--js-flags=--max-old-space-size=512', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost, EXCLUDE 127.0.0.1', '--remote-debugging-address=127.0.0.1', `--remote-debugging-port=${debugPort}`, `--user-data-dir=${path.join(temporary, 'profile')}`, 'about:blank'], {env: process.env, stdio: ['ignore', browserLog, browserLog]});
  for (let attempt = 0; attempt < 40; attempt++) {
    try { driver = await ChromeDriver.connect(debugPort, temporary); break; } catch {}
    await new Promise(resolve => setTimeout(resolve, 150));
  }
  assert(driver, 'Owned system Chrome did not start.');
  const capture = async name => {
    const shot = await driver.send('Page.captureScreenshot', {format: 'jpeg', quality: 92, captureBeyondViewport: false});
    if (process.env.PORTICO_SERVICES_DIAGNOSTIC_ONLY !== 'true') writeFileSync(path.join(output, name + '-build.jpg'), Buffer.from(shot.data, 'base64'));
  };
  if (process.env.PORTICO_SERVICES_SCENARIO === 'auth') {
    await (await import('./services-auth-browser.mjs')).runServicesAuthProof({driver, port, capture, report});
  } else if (process.env.PORTICO_SERVICES_SCENARIO === 'device') {
    for (const [name, width, height, search] of [
      ['account2', 1440, 734, ''], ['account3', 1440, 734, '?state=review'],
      ['account5b', 390, 660, '?qr=1'], ['account5c', 390, 660, '?state=review'],
      ['account6a', 390, 660, '?state=approved'],
    ]) {
      await driver.viewport(width, height);
      await driver.navigate(`http://127.0.0.1:${port}/test/services-device.html${search}`);
      await driver.waitFor("document.querySelector('h1')?.textContent===" + JSON.stringify(name === 'account6a' ? 'Approved' : 'Link a TV'), 45000);
      await driver.evaluate('document.fonts.ready');
      if (name === 'account2') {
        await driver.waitFor("document.activeElement?.name==='code'");
        assert(await driver.evaluate("document.querySelector('button[type=submit]').disabled"));
        await driver.fill('input[name=code]', '7kqm-4txp');
        await driver.waitFor("document.querySelector('input[name=code]').value==='7KQM-4TXP'");
        assert(await driver.evaluate("!document.querySelector('button[type=submit]').disabled"));
        await driver.fill('input[name=code]', '');
      }
      if (name === 'account3') {
        for (let step = 0; step < 2; step++) {
          await driver.send('Input.dispatchKeyEvent', {type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9});
          await driver.send('Input.dispatchKeyEvent', {type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9});
        }
        assert(await driver.evaluate("document.activeElement?.innerText==='Approve device'"));
      }
      await driver.send('Input.dispatchMouseEvent', {type: 'mouseMoved', x: 0, y: 0});
      await capture(name);
      report.states.push({name, ...await driver.evaluate('({text:document.body.innerText,overflow:document.documentElement.scrollWidth>innerWidth,content:[...document.querySelector("[data-auth-content]").children].map(x=>({tag:x.tagName,rect:x.getBoundingClientRect().toJSON()}))})')});
    }
    assert(!report.states.some(state => state.overflow));
    report.checks = {codeInitiallyDisabled: true, completeCodeEnabled: true, codeUppercased: true};
  } else {
  await driver.viewport(1440, 874);
  await driver.navigate(`http://127.0.0.1:${port}/test/services-links.html`);
  await driver.waitFor("document.querySelector('h1')?.textContent==='Choose a new password'", 45000);
  await driver.evaluate('document.fonts.ready');
  const password = runtimeTestPassword(); // Real Strong value; the frame's illustrative dot-count inconsistency is held separately.
  await driver.fill('input[name=new-password]', password); await driver.fill('input[name=confirm-password]', password);
  await driver.waitFor("document.body.innerText.includes('Strong')");
  await driver.clickSelector('button[aria-label="Show password"]');
  assert(await driver.evaluate("document.querySelector('input[name=new-password]').type==='text'"));
  await driver.clickSelector('button[aria-label="Hide password"]');
  assert(await driver.evaluate("document.querySelector('input[name=new-password]').type==='password'"));
  await driver.fill('input[name=confirm-password]', 'different-test-password'); await driver.clickText('Change password');
  await driver.waitFor("document.querySelector('input[name=confirm-password]').getAttribute('aria-invalid')==='true'");
  assert(await driver.evaluate("document.body.innerText.includes('The passwords don’t match.')"));
  await driver.fill('input[name=confirm-password]', password); await driver.fill('input[name=new-password]', 'short');
  await driver.waitFor("document.body.innerText.includes('Too short')");
  assert(await driver.evaluate("document.querySelector('button[type=submit]').disabled"));
  await driver.fill('input[name=new-password]', password);
  await driver.evaluate("document.querySelector('input[name=new-password]').focus();document.querySelector('input[name=new-password]').setSelectionRange(100,100)");
  await driver.send('Input.dispatchMouseEvent', {type: 'mouseMoved', x: 0, y: 0});
  await capture('account14');
  report.checks = {passwordReveal: true, mismatchRefusedWithoutSubmission: true, shortPasswordDisabled: true, strength: 'actual canonical Strong'};
  await driver.viewport(390, 660); await driver.evaluate('document.activeElement.blur();scrollTo(0,0);document.fonts.ready');
  await capture('account17a');
  for (const [state, name, title] of [['reset-done', 'account17b', 'Password changed'], ['verify-elsewhere', 'account17c', 'Email confirmed']]) {
    await driver.navigate(`http://127.0.0.1:${port}/test/services-links.html?state=${state}`);
    await driver.waitFor('document.querySelector("h1")?.textContent===' + JSON.stringify(title), 45000); await driver.evaluate('document.fonts.ready');
    await capture(name);
    const value = await driver.evaluate('({text:document.body.innerText,overflow:document.documentElement.scrollWidth>innerWidth,headerVisible:document.querySelector("header").getClientRects().length>0,elements:[...document.querySelector("[data-auth-content]").children].map(x=>({tag:x.tagName,rect:x.getBoundingClientRect().toJSON(),font:getComputedStyle(x).font}))})');
    assert(!value.overflow && !value.headerVisible); report.states.push({name, ...value});
  }
  }
  if (process.env.PORTICO_SERVICES_SCENARIO !== 'auth') assert(![...driver.requests.values()].some(url => new URL(url).pathname.startsWith('/v1/')), 'These view proofs must not submit to an account API.');
  if (process.env.PORTICO_SERVICES_SCENARIO !== 'auth') assert.equal(driver.events.length, 0, 'Browser diagnostics must be inspected before presentation proof.');
  report.result = 'PASS';
} catch (error) {
  report.result = 'FAIL'; report.error = String(error); process.exitCode = 1;
  if (driver) report.failurePage = await driver.evaluate('({title:document.title,text:document.body.innerText})').catch(() => undefined);
} finally {
  clearInterval(timer); report.peakRSSKiB = peakRSSKiB; report.events = driver?.events ?? [];
  driver?.close(); await stop(chrome); await stop(vite);
  rmSync(temporary, {recursive: true, force: true});
  writeFileSync(path.join(output, 'services-links-browser.log'), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify({result: report.result, error: report.error, peakRSSKiB, qualification: report.qualification}));
}
