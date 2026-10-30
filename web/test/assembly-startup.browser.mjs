import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {createHash, generateKeyPairSync, sign} from 'node:crypto';
import {readFile, stat, rm} from 'node:fs/promises';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {join, resolve} from 'node:path';
import {build} from 'vite';

// Runs the real production main/App/router against an empty browser vault. The
// local origin has NO accounts or credentials. Its synthetic public system and
// fresh signed route proof must reach the real drawn form through native fetch.
const root = fileURLToPath(new URL('..', import.meta.url));
const outDir = join(process.env.TMPDIR, 'assembly-startup-dist');
const {chromium} = await import(pathToFileURL(process.env.PORTICO_PLAYWRIGHT_MODULE).href);
let server, browser;
const keys = generateKeyPairSync('ed25519'), publicKey = keys.publicKey.export({format: 'der', type: 'spki'}).subarray(-32);
const pin = {serverId: 'srv_' + createHash('sha256').update('portico.server.identity.v1\0').update(publicKey).digest('base64url'),
  publicKey: publicKey.toString('base64url'), fingerprint: createHash('sha256').update(publicKey).digest('base64url')};
const requests = {system: 0, proof: 0, otherAPI: 0};
const mime = {'.js': 'text/javascript', '.css': 'text/css', '.woff2': 'font/woff2', '.svg': 'image/svg+xml', '.png': 'image/png'};
try {
  await build({root, configFile: join(root, 'vite.config.ts'), build: {outDir, emptyOutDir: true}});
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, 'http://localhost').pathname;
      if (pathname === '/v1/system') {
        requests.system++;response.writeHead(200, {'Content-Type': 'application/json'});
        response.end(JSON.stringify({id: pin.serverId, name: 'Startup fixture server', version: '1', signIn: 'local', move: null,
          setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []}));return;
      }
      if (pathname === '/v1/networking/identity-proof' && request.method === 'POST') {
        requests.proof++;let body = '';for await (const chunk of request) body += chunk;
        const asked = JSON.parse(body), now = Date.now();
        assert.equal(asked.baseUrl, `http://${request.headers.host}`);
        const payload = Buffer.from(JSON.stringify({kind: 'portico.route-proof', version: '2', ...pin, baseUrl: asked.baseUrl, nonce: asked.nonce,
          issuedAt: new Date(now).toISOString(), expiresAt: new Date(now + 30000).toISOString()}));
        response.writeHead(200, {'Content-Type': 'application/json'});
        response.end(JSON.stringify({payload: payload.toString('base64url'), signature: sign(null, payload, keys.privateKey).toString('base64url')}));return;
      }
      if (pathname.startsWith('/v1/')) {
        requests.otherAPI++;
        response.writeHead(503, {'Content-Type': 'application/json'});
        response.end(JSON.stringify({error: {code: 'unavailable', retryable: true}}));return;
      }
      let file = resolve(outDir, '.' + pathname);
      if (!file.startsWith(outDir + '/') && file !== outDir) {response.writeHead(404);response.end();return;}
      try {if (!(await stat(file)).isFile()) file = join(outDir, 'index.html');} catch {file = join(outDir, 'index.html');}
      const extension = file.slice(file.lastIndexOf('.'));
      response.writeHead(200, {'Content-Type': extension === '.html' ? 'text/html' : mime[extension] ?? 'application/octet-stream', 'Cache-Control': 'no-store'});
      response.end(await readFile(file));
    } catch {response.writeHead(500);response.end();}
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  browser = await chromium.launch({executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ?? '/usr/bin/chromium', headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage']});
  for (const [path, blockedFirstTransition] of [['/', false], ['/sign-in', false], ['/', true]]) {
    const context = await browser.newContext(), page = await context.newPage(), errors = [];
    if (blockedFirstTransition) await page.addInitScript(() => {
      const native = document.startViewTransition.bind(document);
      window.__startupMotion = {blocked: 0, native: 0};
      document.startViewTransition = update => {
        if (document.getElementById('portico-content')) {window.__startupMotion.native++;return native(update);}
        // A native snapshot cannot be a prerequisite for the first routed DOM.
        // This reproduces the observed stage boundary without touching auth.
        window.__startupMotion.blocked++;const pending = new Promise(() => {});
        return {ready: pending, updateCallbackDone: pending, finished: pending, skipTransition() {}};
      };
    });
    page.on('pageerror', error => {errors.push(error.message);});
    const before = {...requests};
    await page.goto(origin + path, {waitUntil: 'networkidle'});
    let mounted = true;
    try {await page.locator('#portico-content').waitFor({timeout: 6000});} catch {mounted = false;}
    let form = true;
    try {await page.locator('input[name="username"]').waitFor({timeout: 6000});} catch {form = false;}
    const observed = await page.evaluate(() => {
      const root = document.getElementById('root'), key = root && Object.keys(root).find(key => key.startsWith('__reactContainer'));
      const start = key && root[key];let router;
      const visited = new Set(), pending = start ? [start.current ?? start] : [];
      while (pending.length && visited.size < 1000) {
        const fiber = pending.pop();if (!fiber || visited.has(fiber)) continue;visited.add(fiber);
        const candidate = fiber.memoizedProps?.router;
        if (candidate?.stores && candidate?.routesById) {router = candidate;break;}
        pending.push(fiber.child, fiber.sibling, fiber.alternate);
      }
      return {path: location.pathname, content: !!document.getElementById('portico-content'), forms: document.forms.length,
        motion: window.__startupMotion,
        rootChildren: [...(root?.children ?? [])].map(element => element.tagName),
        router: router && {isServer: router.isServer, status: router.state.status, isLoading: router.state.isLoading,
          matches: router.state.matches.map(value => ({routeId: value.routeId, status: value.status})),
          pending: router.state.pendingMatches?.length ?? 0, transaction: !!router._tx}};
    });
    const transport = Object.fromEntries(Object.keys(requests).map(key => [key, requests[key] - before[key]]));
    console.log(JSON.stringify({case: path, blockedFirstTransition, mounted, form, observed, transport, errors}));
    await context.close();
    assert.equal(mounted, true, 'production App must mount routed content before any account/API result');
    assert.equal(form, true, 'real native-fetch system/proof requests must reach the drawn Username form');
    assert.ok(transport.system >= 2);assert.ok(transport.proof >= 1);assert.equal(transport.otherAPI, 0, 'no account/auth request is attempted');
    if (blockedFirstTransition) {
      assert.equal(observed.motion.blocked, 0, 'first paint never requests an unavailable native snapshot');
      assert.ok(observed.motion.native > 0, 'the real signed-out redirect still uses native motion after first paint');
    }
    assert.deepEqual(errors, []);
  }
} finally {
  await browser?.close();
  if (server) await new Promise(resolve => server.close(resolve));
  await rm(outDir, {recursive: true, force: true});
}
