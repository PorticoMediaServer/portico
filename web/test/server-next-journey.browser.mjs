// The real web app against the rebuilt server (server-next), by script. It starts the server on a
// fresh data folder and the web development server itself, on localhost, and walks the journeys of
// the areas that have moved: set up the owner, sign in, choose a profile with a PIN, stay signed in
// across a server restart, go back to Who's watching when a PIN changes elsewhere, sign out.
//
//   cd server-next && go build -o /path/to/portico-server-next ./cmd/portico-server
//   cd web && PORTICO_SERVER_NEXT_BIN=/path/to/portico-server-next \
//     [CHROMIUM_PATH=…] [PLAYWRIGHT=/path/to/playwright/index.mjs] node test/server-next-journey.browser.mjs
//
// Each step prints "pass" or "FAIL" and the script carries on where it can, so one run says exactly
// which steps hold. It exits 1 when any step failed.
//
// One stand-in, until the server has the route: GET /v1/preferences is answered here from the
// recorded answer in server/testdata/contracts. The app cannot open without its settings registry.
import {spawn} from 'node:child_process';
import http from 'node:http';
import {mkdtemp, readFile, rm} from 'node:fs/promises';
import {createServer as netServer} from 'node:net';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';

const serverBin = process.env.PORTICO_SERVER_NEXT_BIN;
if (!serverBin) throw new Error('Set PORTICO_SERVER_NEXT_BIN to a build of server-next/cmd/portico-server.');
const {chromium} = await import(process.env.PLAYWRIGHT ?? 'playwright');
const web = dirname(dirname(fileURLToPath(import.meta.url)));
const password = ['journey', 'owner', 'Passphrase', '7'].join('-'), pin = '4821';
const cleanups = [], results = [];

const freePort = () => new Promise((resolve, reject) => {
  const probe = netServer().once('error', reject).listen(0, '127.0.0.1', () => {const {port} = probe.address(); probe.close(() => resolve(port));});
});
const until = async (what, check, tries = 100) => {
  for (let n = 0; n < tries; n++) {if (await check().catch(() => false)) return; await new Promise(resolve => setTimeout(resolve, 100));}
  throw new Error(what + ' did not happen.');
};

const data = await mkdtemp(join(tmpdir(), 'portico-next-journey-'));
cleanups.push(() => rm(data, {recursive: true, force: true}));
const serverPort = await freePort(), serverOrigin = `http://127.0.0.1:${serverPort}`;
let server;
async function startServer() {
  server = spawn(serverBin, ['-data', data, '-listen', `127.0.0.1:${serverPort}`], {stdio: 'ignore'});
  await until('The server starting', async () => (await fetch(serverOrigin + '/v1/system')).ok);
}
async function stopServer() {
  const gone = new Promise(resolve => server.once('exit', resolve));
  server.kill('SIGTERM'); await gone;
}
await startServer();
cleanups.push(() => stopServer());
const system = await (await fetch(serverOrigin + '/v1/system')).json();

// The stand-in for the one missing route; everything else goes to the server untouched.
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

/** What the server was asked on this page, so a step can say which request it waited for. */
function watch(page) {
  const seen = [];
  page.on('response', answer => {const url = new URL(answer.url()); if (url.pathname.startsWith('/v1/')) seen.push(`${answer.request().method()} ${url.pathname} ${answer.status()}`);});
  return {seen, has: prefix => seen.some(line => line.startsWith(prefix))};
}
/** The page's own words for a failure. Areas that have not moved yet show them; a moved area must not. */
const failureWords = /Something went wrong|Something didn.t load|couldn.t load|ran into a problem/i;
async function step(name, run) {
  try {await run(); results.push([name, true]); console.log('pass  ' + name);}
  catch (error) {results.push([name, false]); console.log('FAIL  ' + name + '\n      ' + String(error?.message ?? error).split('\n')[0]);}
}
const ownerToken = async () => {
  const post = (path, body, token) => fetch(serverOrigin + path, {method: 'POST', headers: {'Content-Type': 'application/json', ...(token ? {Authorization: 'Bearer ' + token} : {})}, body: JSON.stringify(body)}).then(answer => answer.json());
  const signIn = await post('/v1/auth/sign-in', {username: 'owner', password, device: {installationId: 'journey-script-installation-000000001', name: 'Journey script'}});
  const chosen = await post(`/v1/profiles/${signIn.profiles.find(profile => profile.isPrimary).id}/select`, {}, signIn.session.accessToken);
  return {token: chosen.accessToken, profiles: signIn.profiles};
};
async function signIn(page) {
  await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
  await page.getByLabel('Username').fill('owner');
  await page.getByLabel('Password', {exact: true}).fill(password);
  await page.getByRole('button', {name: 'Sign in', exact: true}).click();
}
const shell = page => page.getByRole('button', {name: /account menu/});

try {
  const setup = await browser.newContext({viewport: {width: 1440, height: 900}});
  await step('set up the owner with the setup code from the data folder', async () => {
    const page = await setup.newPage(), requests = watch(page);
    await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
    await page.getByRole('radio', {name: /Local sign-in/}).check();
    await page.getByLabel('Username').fill('owner');
    await page.getByLabel('Password', {exact: true}).fill(password);
    await page.getByLabel('Confirm password').fill(password);
    await page.getByRole('button', {name: 'Create owner'}).click();
    await page.getByLabel('Setup code').fill('AAAA-AAAA');
    await page.getByRole('button', {name: 'Continue'}).click();
    await until('The wrong setup code being refused', async () => requests.has('POST /v1/setup/owner 403'));
    await page.getByLabel('Setup code').fill((await readFile(join(data, 'setup-code.txt'), 'utf8')).trim());
    await page.getByRole('button', {name: 'Continue'}).click();
    await page.getByText('Add your first library').waitFor();
    if (!requests.has('POST /v1/setup/owner 200')) throw new Error('The owner was not created.');
    if (failureWords.test(await page.locator('body').innerText())) throw new Error('The page shows a failure.');
  });
  await setup.close();

  // A second profile with a PIN, made the way another device would make it.
  const made = await fetch(serverOrigin + '/v1/profiles', {method: 'POST', headers: {'Content-Type': 'application/json', Authorization: 'Bearer ' + (await ownerToken()).token}, body: JSON.stringify({name: 'Kid', color: 'coral', pin})});
  if (!made.ok) throw new Error('The script could not add a profile: ' + made.status);

  const context = await browser.newContext({viewport: {width: 1440, height: 900}});
  const page = await context.newPage(), requests = watch(page);
  await step('sign in with the password and reach Who\'s watching', async () => {
    await signIn(page);
    await page.getByRole('button', {name: /Kid/}).waitFor();
    if (!requests.has('POST /v1/auth/sign-in 200')) throw new Error('Sign-in was not accepted.');
  });
  await step('a wrong PIN is refused in plain words', async () => {
    await page.getByRole('button', {name: /Kid/}).click();
    await page.getByLabel('Four-digit PIN').fill('0000');
    await page.getByText('That PIN isn’t right').waitFor();
  });
  await step('the right PIN opens the profile and the app', async () => {
    // A wrong PIN makes that profile wait two seconds on this device before the next try.
    await page.waitForTimeout(2500);
    await page.getByLabel('Four-digit PIN').fill(pin);
    await shell(page).waitFor();
    if (!/Kid/.test(await shell(page).getAttribute('aria-label') ?? '')) throw new Error('The app opened as someone else.');
  });
  await step('after a server restart nobody is signed out: the sign-in is renewed', async () => {
    await stopServer(); await startServer();
    await page.reload({waitUntil: 'domcontentloaded'});
    await page.getByRole('button', {name: /Kid/}).waitFor();
    if (!requests.has('POST /v1/auth/refresh 200')) throw new Error('The sign-in was not renewed.');
    await page.getByRole('button', {name: /Kid/}).click();
    await page.getByLabel('Four-digit PIN').fill(pin);
    await shell(page).waitFor();
  });
  await step('a PIN changed on another device sends this one back to Who\'s watching, still signed in', async () => {
    const owner = await ownerToken(), kid = owner.profiles.find(profile => profile.name === 'Kid');
    const changed = await fetch(`${serverOrigin}/v1/profiles/${kid.id}/pin`, {method: 'PUT', headers: {'Content-Type': 'application/json', Authorization: 'Bearer ' + owner.token}, body: JSON.stringify({pin})});
    if (!changed.ok) throw new Error('The PIN could not be changed: ' + changed.status);
    const before = requests.seen.length;
    await page.reload({waitUntil: 'domcontentloaded'});
    await page.getByRole('button', {name: /Kid/}).waitFor();
    if (!requests.seen.slice(before).some(line => line.startsWith('POST /v1/auth/refresh 200'))) throw new Error('The sign-in was not renewed.');
    if (await page.getByLabel('Username').count()) throw new Error('The device was signed out.');
  });
  await step('the main profile opens without a PIN', async () => {
    await page.getByRole('button', {name: /owner/}).click();
    await shell(page).waitFor();
  });
  await step('sign out returns to sign-in, and stays signed out after a reload', async () => {
    await shell(page).click();
    await page.getByText('Sign out', {exact: true}).click();
    await page.getByLabel('Username').waitFor();
    if (!requests.has('DELETE /v1/auth/session 204')) throw new Error('The server was not told.');
    await page.reload({waitUntil: 'domcontentloaded'});
    await page.getByLabel('Username').waitFor();
  });
} finally {
  for (const cleanup of cleanups.reverse()) await Promise.resolve().then(cleanup).catch(() => {});
}
const failed = results.filter(([, ok]) => !ok).length;
console.log(`${results.length - failed} of ${results.length} steps passed.`);
process.exit(failed ? 1 : 0);
