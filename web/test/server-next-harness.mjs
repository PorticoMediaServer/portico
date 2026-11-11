// What the browser journeys against the rebuilt server (server-next) share: a server on a fresh data folder
// (optionally seeded with a small library first), the web development server in front of it, headless
// Chromium, a few direct requests a script makes "from another device", and the pass/FAIL step log.
//
//   PORTICO_SERVER_NEXT_BIN=/path/to/portico-server [PORTICO_SEED_BIN=/path/to/portico-seed]
//   [CHROMIUM_PATH=…] [PLAYWRIGHT=/path/to/playwright/index.mjs]
//
// The server is told to look titles up at a closed local port, never the real TMDB.
import {spawn} from 'node:child_process';
import {createHmac} from 'node:crypto';
import http from 'node:http';
import {mkdtemp, readFile, rm} from 'node:fs/promises';
import {createServer as netServer} from 'node:net';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';

export const web = dirname(dirname(fileURLToPath(import.meta.url)));
/** The page's own words for a failure. */
export const failureWords = /Something went wrong|Something didn.t load|couldn.t load|ran into a problem/i;
export const until = async (what, check, tries = 100) => {
  for (let n = 0; n < tries; n++) {if (await check().catch(() => false)) return; await new Promise(resolve => setTimeout(resolve, 100));}
  throw new Error(what + ' did not happen.');
};
const freePort = () => new Promise((resolve, reject) => {
  const probe = netServer().once('error', reject).listen(0, '127.0.0.1', () => {const {port} = probe.address(); probe.close(() => resolve(port));});
});
const run = (bin, args) => new Promise((resolve, reject) => {
  const child = spawn(bin, args, {stdio: 'ignore'});
  child.once('error', reject).once('exit', code => code === 0 ? resolve() : reject(new Error(`${bin} exited ${code}`)));
});

/** The current six-digit code of an authenticator set up with `secret` (base32, RFC 6238). */
export function totp(secret, at = Date.now()) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567', clean = secret.replace(/[\s=-]/g, '').toUpperCase();
  let bits = '';
  for (const ch of clean) bits += alphabet.indexOf(ch).toString(2).padStart(5, '0');
  const key = Buffer.from(bits.match(/.{8}/g).map(byte => parseInt(byte, 2)));
  const counter = Buffer.alloc(8); counter.writeBigUInt64BE(BigInt(Math.floor(at / 30000)));
  const mac = createHmac('sha1', key).update(counter).digest(), offset = mac[19] & 15;
  return String((mac.readUInt32BE(offset) & 0x7fffffff) % 1_000_000).padStart(6, '0');
}

export async function startJourney({seedItems = 0} = {}) {
  const serverBin = process.env.PORTICO_SERVER_NEXT_BIN;
  if (!serverBin) throw new Error('Set PORTICO_SERVER_NEXT_BIN to a build of server-next/cmd/portico-server.');
  const {chromium} = await import(process.env.PLAYWRIGHT ?? 'playwright');
  const cleanups = [], results = [];
  const data = await mkdtemp(join(tmpdir(), 'portico-next-journey-'));
  cleanups.push(() => rm(data, {recursive: true, force: true}));
  if (seedItems) {
    if (!process.env.PORTICO_SEED_BIN) throw new Error('Set PORTICO_SEED_BIN to a build of server-next/cmd/portico-seed.');
    await run(process.env.PORTICO_SEED_BIN, ['-db', join(data, 'main.db'), '-items', String(seedItems), '-phase', 'import']);
  }
  let serverPort = 0;
  while (serverPort <= 20000) serverPort = await freePort();
  const serverOrigin = `http://127.0.0.1:${serverPort}`;
  let server;
  const startServer = async () => {
    server = spawn(serverBin, ['-data', data, '-listen', `127.0.0.1:${serverPort}`], {stdio: 'ignore', env: {...process.env, PORTICO_TMDB_ORIGIN: 'http://127.0.0.1:9'}});
    await until('The server starting', async () => (await fetch(serverOrigin + '/v1/system')).ok);
  };
  const stopServer = async () => {const gone = new Promise(resolve => server.once('exit', resolve)); server.kill('SIGTERM'); await gone;};
  await startServer();
  cleanups.push(() => stopServer());
  const system = await (await fetch(serverOrigin + '/v1/system')).json();

  // One stand-in, until the apps read the server's settings answer: GET /v1/preferences is answered from the
  // recorded answer in server/testdata/contracts. Everything else goes to the server untouched.
  const recorded = JSON.parse(await readFile(join(web, '..', 'server', 'testdata', 'contracts', 'get-preferences.json'), 'utf8')).response;
  const preferences = JSON.stringify({scope: {serverId: system.id, viewerFence: recorded.scope.viewerFence}, data: recorded.data});
  const front = http.createServer((request, response) => {
    if (request.method === 'GET' && request.url.startsWith('/v1/preferences')) {response.writeHead(200, {'Content-Type': 'application/json'}); response.end(preferences); return;}
    const forward = http.request({host: '127.0.0.1', port: serverPort, path: request.url, method: request.method, headers: request.headers},
      answer => {response.writeHead(answer.statusCode, answer.headers); answer.pipe(response);});
    forward.on('error', () => {response.writeHead(502); response.end();});
    request.pipe(forward);
  });
  await new Promise(resolve => front.listen(0, '127.0.0.1', resolve));
  cleanups.push(() => new Promise(resolve => {front.closeAllConnections(); front.close(resolve);}));

  process.env.PORTICO_DEV_SERVER_URL = `http://127.0.0.1:${front.address().port}`;
  const {createServer} = await import('vite');
  const vite = await createServer({root: web, logLevel: 'error', server: {host: '127.0.0.1', port: await freePort(), strictPort: true}});
  await vite.listen();
  cleanups.push(() => vite.close());
  const origin = `http://127.0.0.1:${vite.config.server.port}`;
  const browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ['--no-sandbox']});
  cleanups.push(() => browser.close());
  // The development server compiles the app on the first visit, which takes minutes on a busy machine. Do that once
  // here, so a step's own wait measures the app and not the compiler.
  const warm = await browser.newPage();
  await warm.goto(origin + '/', {waitUntil: 'domcontentloaded', timeout: 240_000});
  await warm.locator('input, button').first().waitFor({timeout: 240_000});
  await warm.close();

  /** One request as another device would send it. Answers `{status, body}`. */
  const call = async (method, path, body, token) => {
    const answer = await fetch(serverOrigin + path, {method, headers: {'Content-Type': 'application/json', ...(token ? {Authorization: 'Bearer ' + token} : {})}, body: body === undefined ? undefined : JSON.stringify(body)});
    const text = await answer.text();
    return {status: answer.status, body: text ? JSON.parse(text) : undefined};
  };
  let devices = 0;
  /** Signs `username` in from a new device and opens a profile (the main one unless named): answers its token and profiles. */
  const signInAs = async (username, password, profileName, pin) => {
    const device = {installationId: `journey-script-installation-${String(++devices).padStart(12, '0')}`, name: 'Journey script'};
    const signedIn = await call('POST', '/v1/auth/sign-in', {username, password, device});
    if (signedIn.status !== 200 || !signedIn.body.session) throw new Error(`${username} could not sign in: ${signedIn.status}`);
    const profile = signedIn.body.profiles.find(p => profileName ? p.name === profileName : p.isPrimary);
    const chosen = await call('POST', `/v1/profiles/${profile.id}/select`, pin ? {pin} : {}, signedIn.body.session.accessToken);
    if (chosen.status !== 200) throw new Error(`${username} could not open ${profile.name}: ${chosen.status}`);
    return {token: chosen.body.accessToken, profiles: signedIn.body.profiles, account: signedIn.body.account};
  };
  /** Sets up the owner straight away with the setup code from the data folder. */
  const setUpOwner = async password => {
    const setupCode = (await readFile(join(data, 'setup-code.txt'), 'utf8')).trim();
    const made = await call('POST', '/v1/setup/owner', {setupCode, username: 'owner', password, device: {installationId: 'journey-script-installation-owner00000', name: 'Journey script'}});
    if (made.status !== 200) throw new Error('The owner could not be set up: ' + made.status);
  };

  /** What the server was asked on this page, so a step can say which request it waited for. */
  let lastPage, lastSeen = [];
  const watch = page => {
    const seen = []; lastPage = page; lastSeen = seen;
    if (process.env.SHOTS) page.on('console', message => {if (message.type() === 'error' && !/Failed to load resource/.test(message.text())) seen.push('CONSOLE ' + message.text().slice(0, 300));});
    page.on('response', answer => {const url = new URL(answer.url()); if (url.pathname.startsWith('/v1/')) seen.push(`${answer.request().method()} ${url.pathname} ${answer.status()}`);});
    return {seen, has: prefix => seen.some(line => line.startsWith(prefix)), since: (from, prefix) => seen.slice(from).some(line => line.startsWith(prefix))};
  };
  /** `page` is the page a failed step's picture is taken of (the last watched page otherwise). */
  const step = async (name, body, page) => {
    if (page) lastPage = page;
    try {await body(); results.push([name, true]); console.log('pass  ' + name);}
    catch (error) {
      results.push([name, false]); console.log('FAIL  ' + name + '\n      ' + String(error?.message ?? error).split('\n')[0]);
      // SHOTS=<folder> keeps a picture of the page each failed step left behind.
      if (process.env.SHOTS) console.log('      last requests: ' + lastSeen.filter(line => !line.includes('/v1/guide/')).slice(-8).join(' | '));
      if (process.env.SHOTS && lastPage) await lastPage.screenshot({path: join(process.env.SHOTS, `step-${results.length}.png`), fullPage: true}).catch(() => {});
    }
  };
  /** Signs in on the page and opens a profile, entering its PIN when asked. `code` gives a two-step code when the
   * account asks for one. */
  const signInPage = async (page, username, password, profileName, pin, code) => {
    await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
    await page.getByLabel('Username').fill(username);
    await page.getByLabel('Password', {exact: true}).fill(password);
    await page.getByRole('button', {name: 'Sign in', exact: true}).click();
    const shell = page.getByRole('button', {name: /account menu/});
    if (code) {
      await page.locator('input[autocomplete="one-time-code"]').first().fill(await code());
      await page.getByRole('button', {name: 'Continue'}).click();
    }
    if (profileName) {
      const choice = page.getByRole('button', {name: new RegExp(profileName)});
      await shell.or(choice).first().waitFor();
      if (!await shell.isVisible()) {await choice.click(); if (pin) await page.getByLabel('Four-digit PIN').fill(pin);}
    }
    await shell.waitFor();
  };
  /** Opens a Settings section the way a person does: the account menu, Settings, then the section. */
  const openSettings = async (page, section) => {
    await page.getByRole('button', {name: /account menu/}).click();
    await page.getByText(/^Settings$/).first().click();
    if (section) await page.getByRole('link', {name: section, exact: true}).or(page.getByRole('button', {name: section, exact: true})).first().click();
  };
  const finish = async () => {
    for (const cleanup of cleanups.reverse()) await Promise.resolve().then(cleanup).catch(() => {});
    const failed = results.filter(([, ok]) => !ok).length;
    console.log(`${results.length - failed} of ${results.length} steps passed.`);
    process.exit(failed ? 1 : 0);
  };
  return {data, serverOrigin, origin, browser, call, signInAs, setUpOwner, watch, step, signInPage, openSettings, startServer, stopServer, finish};
}
