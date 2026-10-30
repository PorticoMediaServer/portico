// Remote-only mounted regression: no service credentials or live mutations.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {existsSync} from 'node:fs';
import net from 'node:net';
import {resolve} from 'node:path';
const modulePath = process.env.PORTICO_PLAYWRIGHT_MODULE;
if (!modulePath) throw new Error('PORTICO_PLAYWRIGHT_MODULE is required');
const {chromium} = await import(modulePath);
const executablePath = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, '/usr/bin/chromium', '/usr/bin/google-chrome'].find(path => path && existsSync(path));
if (!executablePath) throw new Error('A runner Chromium is required');
const portServer = net.createServer();
await new Promise(resolve => portServer.listen(0, '127.0.0.1', resolve));
const port = portServer.address().port;
await new Promise(resolve => portServer.close(resolve));
let app, browser;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
try {
  app = spawn(process.execPath, ['../node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', String(port)], {cwd: process.cwd(), detached: true, stdio: ['ignore', 'pipe', 'pipe']});
  let log = '';app.stdout.on('data', bytes => {log += bytes;});app.stderr.on('data', bytes => {log += bytes;});
  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; ; i++) {
    try {if ((await fetch(base)).ok) break;} catch {}
    if (i > 120 || app.exitCode !== null) throw new Error('Vite startup failed: ' + log.slice(-1000));
    await sleep(200);
  }
  browser = await chromium.launch({executablePath, headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage']});
  for (const mode of ['failure', 'limit', 'retired', 'readonly']) {
    const context = await browser.newContext({viewport: {width: mode === 'failure' ? 390 : 1440, height: 1000}});
    try {
      const page = await context.newPage(), errors = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto(`${base}/test/fixtures/profile-add.html?mode=${mode}`);
      await page.locator('[data-ready]').waitFor({state: 'attached'});
      if (mode === 'readonly') {
        assert.equal(await page.getByRole('dialog').count(), 0);
        assert.equal((await page.evaluate(() => window.profileTest.facts())).writes, 0);
      } else {
        await page.getByRole('button', {name: 'Add a profile', exact: true}).click();
        await page.getByRole('textbox', {name: 'Name', exact: true}).fill('Created in mounted test');
        await page.getByRole('button', {name: 'Save', exact: true}).click();
        await page.waitForFunction(() => window.profileTest.facts().writes === 1);
        await page.locator('form').evaluate(form => form.dispatchEvent(new Event('submit', {bubbles: true, cancelable: true})));
        assert.equal((await page.evaluate(() => window.profileTest.facts())).writes, 1);
        if (mode === 'retired') await page.evaluate(() => window.profileTest.retire());
        await page.evaluate(() => window.profileTest.release());
        if (mode === 'failure') {
          await page.locator('[data-failure]').waitFor({state: 'attached'});
          const facts = await page.evaluate(() => window.profileTest.facts());
          assert.equal(facts.failures, 1);assert.equal(facts.closes, 1);
        } else if (mode === 'limit') {
          await page.waitForFunction(() => window.profileTest.facts().count === 8);
          assert.equal((await page.evaluate(() => window.profileTest.facts())).failures, 0);
        } else {
          await page.waitForFunction(() => window.profileTest.facts().count === 2);
          const facts = await page.evaluate(() => window.profileTest.facts());
          assert.equal(facts.failures, 0);assert.equal(facts.closes, 0);
        }
        assert.equal(await page.getByRole('dialog').count(), 0);
      }
      assert.deepEqual(errors, []);console.log(`PASS mounted profile ${mode}`);
    } finally {await context.close();}
  }
} finally {
  await browser?.close();
  if (app && app.exitCode === null) {
    process.kill(-app.pid, 'SIGTERM');
    await Promise.race([new Promise(resolve => app.once('exit', resolve)), sleep(3000)]);
    try {process.kill(-app.pid, 'SIGKILL');} catch {}
  }
}
