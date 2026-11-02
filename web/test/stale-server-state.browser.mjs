// A browser that signed in to a server, which is then reset or replaced at the same address (a fresh
// deploy of demo.getportico.tv), must reach sign-in on its next load, never the page failure.
//
// Runs the real web build on a real development server, in WebKit and Chromium:
//   cd server && go build -tags devtrust -o /tmp/portico-devtrust ./cmd/server
//   cd web && npx vite build --outDir /tmp/portico-web
//   PORTICO_SERVER_BIN=/tmp/portico-devtrust PORTICO_WEB_DIR=/tmp/portico-web \
//     [WEBKIT_PATH=…] [CHROMIUM_PATH=…] [PLAYWRIGHT=/path/to/playwright/index.mjs] \
//     node test/stale-server-state.browser.mjs
//
// No password is typed or stored: the sign-in form's request is answered by the server's
// loopback-only devtrust route (POST /v1/dev/e2e/sign-in), which returns exactly a direct
// sign-in's answer, so the browser saves the session through its real path.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdtemp, rm} from 'node:fs/promises';
import {createServer} from 'node:net';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

const serverBin = process.env.PORTICO_SERVER_BIN, webDir = process.env.PORTICO_WEB_DIR;
if (!serverBin || !webDir) throw new Error('Set PORTICO_SERVER_BIN (a devtrust build) and PORTICO_WEB_DIR (a web build).');
const {webkit, chromium} = await import(process.env.PLAYWRIGHT ?? 'playwright');
const engines = (process.env.ENGINES ?? 'webkit,chromium').split(',');
const launchOptions = {
  webkit: {executablePath: process.env.WEBKIT_PATH || undefined},
  chromium: {executablePath: process.env.CHROMIUM_PATH || undefined, args: ['--no-sandbox']},
};
const scratch = [];

const freePort = () => new Promise((resolve, reject) => {
  const probe = createServer().once('error', reject).listen(0, '127.0.0.1', () => {const {port} = probe.address(); probe.close(() => resolve(port));});
});

/** A throwaway server whose state folder name marks it as a development e2e server. */
async function startServer(port) {
  const data = await mkdtemp(join(tmpdir(), 'portico-stale-e2e-'));
  scratch.push(data);
  const child = spawn(serverBin, [], {stdio: 'ignore', env: {...process.env, PORTICO_DATA_DIR: data, PORTICO_BIND: `127.0.0.1:${port}`, PORTICO_WEB_DIR: webDir, PORTICO_DISCOVERY: 'off'}});
  const origin = `http://127.0.0.1:${port}`;
  for (let attempt = 0; attempt < 240; attempt++) {
    if (child.exitCode !== null) throw new Error(`The server exited with ${child.exitCode}.`);
    try {
      const response = await fetch(origin + '/v1/system');
      if (response.ok) {
        const system = await response.json();
        const e2e = await fetch(origin + '/v1/dev/e2e');
        if (!e2e.ok) throw new Error('This server has no development e2e owner; build it with -tags devtrust.');
        return {origin, id: system.id, stop: () => new Promise(done => {child.once('exit', done); child.kill('SIGTERM');})};
      }
    } catch (error) {if (/devtrust/.test(String(error))) throw error;}
    await new Promise(done => setTimeout(done, 500));
  }
  child.kill('SIGKILL');
  throw new Error('The server did not start.');
}

async function run(engine) {
  const port = await freePort();
  const profile = await mkdtemp(join(tmpdir(), `portico-stale-${engine}-`));
  scratch.push(profile);
  const type = engine === 'webkit' ? webkit : chromium;
  const open = () => type.launchPersistentContext(profile, {...launchOptions[engine], headless: true, viewport: {width: 1440, height: 900}});
  let server = await startServer(port), context;
  try {
    // 1. Sign in through the real form; the saved session belongs to the first server.
    context = await open();
    let page = context.pages()[0] ?? await context.newPage();
    await page.route('**/v1/direct/sign-in', async route => {
      const answer = await route.fetch({url: server.origin + '/v1/dev/e2e/sign-in', method: 'POST', postData: JSON.stringify({username: 'e2e-owner'}), headers: {'content-type': 'application/json'}});
      await route.fulfill({response: answer});
    });
    await page.goto(server.origin + '/');
    await page.waitForURL(/\/sign-in/, {timeout: 30000});
    await page.getByLabel('Username').fill('e2e-owner');
    await page.getByLabel('Password', {exact: true}).fill('not-a-password');
    const home = page.waitForResponse(response => new URL(response.url()).pathname === '/v1/home' && response.ok(), {timeout: 30000});
    await page.getByRole('button', {name: 'Sign in', exact: true}).click();
    await home;
    assert.equal(new URL(page.url()).pathname, '/');
    await context.close(); context = undefined;

    // 2. The server is reset: a new state folder and identity at the same address.
    const first = server.id;
    await server.stop();
    server = await startServer(port);
    assert.notEqual(server.id, first, 'the replacement must prove a different server identity');

    // 3. The next load reaches the saved server's identity page on sign-in, never the page failure.
    context = await open();
    page = context.pages()[0] ?? await context.newPage();
    await page.goto(server.origin + '/');
    await page.waitForURL(/\/sign-in/, {timeout: 30000});
    const forget = page.getByRole('button', {name: /^Forget /});
    await forget.waitFor({timeout: 30000});
    assert.equal(await page.getByText("Something didn't load").count(), 0, `${engine}: no page failure`);
    assert.equal(await page.getByRole('button', {name: 'Choose another server'}).count(), 1);

    // 4. On a page this server served, Forget signs in to the server actually here, at once:
    //    no chooser, no empty address form.
    await forget.click();
    await page.getByRole('alertdialog').getByRole('button', {name: /Forget/}).click();
    await page.getByLabel('Password', {exact: true}).waitFor({timeout: 30000});
    assert.equal(new URL(page.url()).pathname, '/sign-in', `${engine}: Forget stays on sign-in`);
    const text = await page.locator('body').innerText();
    assert.doesNotMatch(text, /Something didn.t load|Use a local server/);
    assert.doesNotMatch(text, /Try again in 0:00/, `${engine}: no zero countdown`);

    // 5. The "Use a local server" form on a server's own page starts with this page's address.
    await page.goto(server.origin + '/servers?local=true');
    const address = page.getByLabel('Server address');
    await address.waitFor({timeout: 30000});
    assert.equal(await address.inputValue(), server.origin, `${engine}: address prefilled`);
    console.log(`ok ${engine}: stale sign-in → identity page → forget → sign-in, address prefilled`);
  } finally {
    await context?.close().catch(() => {});
    await server.stop();
  }
}

try {
  for (const engine of engines) await run(engine);
} finally {
  for (const path of scratch) await rm(path, {recursive: true, force: true});
}
