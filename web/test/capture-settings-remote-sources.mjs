// Remote-only comparison harness for the Server Remote sources screen.
// It starts the exact built app against a fresh disposable server, signs in with
// throwaway credentials, and captures the approved read-only Settings mock at
// the same 1440x1000 logical viewport and state. No reference bytes are written
// to the repository; only this browser session and its own output directory.
//
// Run on the external runner:
//   PORTICO_RUNNER_OUTPUT=/abs/out PORTICO_RUNNER_SECONDARY_SOURCE=/abs/portico-internal \
//   PORTICO_SERVER_BIN=/abs/dist/portico-server PORTICO_CHROMIUM=/usr/bin/chromium \
//   node web/test/capture-settings-remote-sources.mjs
import {spawn} from 'node:child_process';
import {createServer} from 'node:http';
import {mkdir, mkdtemp, readFile, rm, writeFile} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {extname, join, resolve} from 'node:path';
import {randomBytes, randomUUID} from 'node:crypto';
import net from 'node:net';
import {networkInterfaces} from 'node:os';

const output = process.env.PORTICO_RUNNER_OUTPUT;
const secondary = process.env.PORTICO_RUNNER_SECONDARY_SOURCE;
const binary = process.env.PORTICO_SERVER_BIN;
const chromium = process.env.PORTICO_CHROMIUM || ['/usr/bin/chromium', '/snap/bin/chromium', '/usr/bin/google-chrome'].find(existsSync);
if (!output || !secondary || !binary) throw new Error('PORTICO_RUNNER_OUTPUT, PORTICO_RUNNER_SECONDARY_SOURCE and PORTICO_SERVER_BIN are required');
const root = process.cwd();
const design = resolve(secondary, 'design/ui-v1');
await mkdir(output, {recursive: true});

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const freePort = async () => { const server = net.createServer(); await new Promise(resolve => server.listen(0, '127.0.0.1', resolve)); const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port; };
const types = {'.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.png': 'image/png', '.webp': 'image/webp', '.woff2': 'font/woff2', '.json': 'application/json'};

function staticServer(directory) {
  return createServer(async (request, response) => {
    try {
      const path = decodeURIComponent(new URL(request.url, 'http://127.0.0.1').pathname);
      const file = resolve(directory, path === '/' ? 'index.html' : path.slice(1));
      if (!file.startsWith(directory + '/')) throw new Error('outside');
      const body = await readFile(file);
      response.writeHead(200, {'Content-Type': types[extname(file)] || 'application/octet-stream', 'Cache-Control': 'no-store'});
      response.end(body);
    } catch { response.writeHead(404); response.end('not found'); }
  });
}

/** Minimal dependency-free CDP client (same protocol Chrome exposes to the smoke harness). */
class Driver {
  static async connect(port) {
    for (let attempt = 0; attempt < 100; attempt++) {
      try {
        const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
        const target = targets.find(item => item.type === 'page');
        if (target) {const driver = new Driver(target.webSocketDebuggerUrl); await driver.ready; return driver;}
      } catch {}
      await sleep(150);
    }
    throw new Error('Chromium did not expose a page target');
  }
  constructor(url) {
    this.sequence = 0; this.pending = new Map();
    this.socket = new WebSocket(url);
    this.ready = new Promise((resolve, reject) => {this.socket.onopen = resolve; this.socket.onerror = reject;});
    this.socket.onmessage = event => {
      const message = JSON.parse(event.data);
      if (!message.id) {
        if (message.method === 'Runtime.exceptionThrown') console.error('PAGE EXCEPTION', JSON.stringify(message.params.exceptionDetails).slice(0, 4000));
        if (message.method === 'Runtime.consoleAPICalled' && message.params.type === 'error') console.error('PAGE CONSOLE', JSON.stringify(message.params.args).slice(0, 2000));
        return;
      }
      const task = this.pending.get(message.id); if (!task) return;
      this.pending.delete(message.id); clearTimeout(task.timer);
      if (message.error) task.reject(new Error(`${message.error.code}: ${message.error.message}`)); else task.resolve(message.result);
    };
  }
  send(method, params = {}) {
    const id = ++this.sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {this.pending.delete(id); reject(new Error('CDP timeout: ' + method));}, 20000);
      this.pending.set(id, {resolve, reject, timer});
      this.socket.send(JSON.stringify({id, method, params}));
    });
  }
  async evaluate(expression) {
    const result = await this.send('Runtime.evaluate', {expression, awaitPromise: true, returnByValue: true});
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || 'evaluation failed');
    return result.result.value;
  }
  async navigate(url) {const result = await this.send('Page.navigate', {url}); if (result.errorText) throw new Error(result.errorText);}
  async waitFor(expression, timeout = 20000) {
    const end = Date.now() + timeout;
    while (Date.now() < end) {
      try {if (await this.evaluate(`!!(${expression})`)) return true;} catch {}
      await sleep(120);
    }
    throw new Error('Timed out waiting for ' + expression);
  }
  async viewport(width, height) {await this.send('Emulation.setDeviceMetricsOverride', {width, height, screenWidth: width, screenHeight: height, deviceScaleFactor: 1, mobile: false});}
  async screenshot(file) {const {data} = await this.send('Page.captureScreenshot', {format: 'png', fromSurface: true, captureBeyondViewport: false}); await writeFile(file, Buffer.from(data, 'base64'));}
}

/** A local, disposable WebDAV origin for the real source setup. It lists one
 * small file and serves byte ranges; no product data or credential is used. */
const davBytes = Buffer.alloc(65536, 7);
function davOrigin() {
  return createServer((request, response) => {
    const path = new URL(request.url, 'http://127.0.0.1').pathname;
    if (request.method === 'OPTIONS') { response.writeHead(200, {'DAV': '1,2', 'Allow': 'OPTIONS, PROPFIND, GET, HEAD', 'MS-Author-Via': 'DAV'}); response.end(); return; }
    if (request.method === 'PROPFIND') {
      const base = path.endsWith('/') ? path : path + '/';
      const fileHref = `${base}sample.mp4`;
      const prop = (href, collection) => `<D:response><D:href>${href}</D:href><D:propstat><D:prop><D:resourcetype>${collection ? '<D:collection/>' : ''}</D:resourcetype><D:displayname>${collection ? 'Archive' : 'sample.mp4'}</D:displayname><D:getcontentlength>${collection ? 0 : davBytes.length}</D:getcontentlength>${collection ? '' : '<D:getcontenttype>video/mp4</D:getcontenttype>'}<D:getetag>"${collection ? 'root' : 'sample'}"</D:getetag><D:getlastmodified>Sat, 24 Oct 2026 00:00:00 GMT</D:getlastmodified></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`;
      const body = path === fileHref ? prop(fileHref, false) : prop(base, true) + prop(fileHref, false);
      response.writeHead(207, {'Content-Type': 'application/xml; charset=utf-8'});
      response.end(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">${body}</D:multistatus>`);
      return;
    }
    if (request.method === 'HEAD') { response.writeHead(200, {'Content-Length': davBytes.length, 'Content-Type': 'video/mp4', 'Accept-Ranges': 'bytes'}); response.end(); return; }
    if (request.method === 'GET') {
      const range = /^bytes=(\d*)-(\d*)$/.exec(request.headers.range ?? '');
      if (range) {
        const start = Math.max(0, Math.min(Number(range[1] || 0), davBytes.length - 1));
        const end = Math.max(start, Math.min(Number(range[2] || davBytes.length - 1), davBytes.length - 1));
        response.writeHead(206, {'Content-Length': end - start + 1, 'Content-Range': `bytes ${start}-${end}/${davBytes.length}`, 'Accept-Ranges': 'bytes', 'Content-Type': 'video/mp4'});
        response.end(davBytes.subarray(start, end + 1));
      } else { response.writeHead(200, {'Content-Length': davBytes.length, 'Content-Type': 'video/mp4', 'Accept-Ranges': 'bytes'}); response.end(davBytes); }
      return;
    }
    response.writeHead(405); response.end();
  });
}
const dav = davOrigin();
await new Promise(resolve => dav.listen(0, '0.0.0.0', resolve));
const davPort = dav.address().port;
const davHost = Object.values(networkInterfaces()).flat().find(address => address && address.family === 'IPv4' && !address.internal)?.address ?? '127.0.0.1';
const appPort = await freePort(), designPort = await freePort(), chromePort = await freePort();
const state = await mkdtemp(join(process.env.TMPDIR || '/tmp', 'settings-state-'));
const media = await mkdtemp(join(process.env.TMPDIR || '/tmp', 'settings-media-'));
const profile = await mkdtemp(join(process.env.TMPDIR || '/tmp', 'settings-chrome-'));
const credentials = {username: 'demo', password: 'Test-' + randomBytes(12).toString('base64url') + '9x'};
const referenceScreens = resolve(design, 'settings.html');
let app, reference, browser, driver;
const log = [];
const note = value => {log.push(value); console.log(value);};
let failure;

try {
  if (!(await readFile(referenceScreens)).length) throw new Error('reference mock is missing');
  reference = staticServer(design);
  await new Promise(resolve => reference.listen(designPort, '127.0.0.1', resolve));
  app = spawn(binary, [], {cwd: root, stdio: ['ignore', 'pipe', 'pipe'], env: {...process.env,
    PORTICO_DATA_DIR: state, PORTICO_WEB_DIR: resolve(root, 'web/dist'), PORTICO_BIND: `127.0.0.1:${appPort}`,
    PORTICO_DISCOVERY: '0', PORTICO_FFMPEG: process.env.PORTICO_FFMPEG || '/usr/bin/ffmpeg', PORTICO_FFPROBE: process.env.PORTICO_FFPROBE || '/usr/bin/ffprobe',
    HTTP_PROXY: 'http://127.0.0.1:9', HTTPS_PROXY: 'http://127.0.0.1:9', ALL_PROXY: 'http://127.0.0.1:9', NO_PROXY: '127.0.0.1,localhost'}});
  let appLog = ''; app.stdout.on('data', bytes => {appLog += bytes;}); app.stderr.on('data', bytes => {appLog += bytes;});
  const base = `http://127.0.0.1:${appPort}`;
  for (let attempt = 0; ; attempt++) {
    try {const response = await fetch(base + '/v1/system'); if (response.ok) break;} catch {}
    if (attempt > 160 || app.exitCode !== null) throw new Error('server did not start\n' + appLog.split('\n').slice(-12).join('\n'));
    await sleep(150);
  }
  const json = async (route, options = {}) => {
    const headers = {Accept: 'application/json'}; if (options.body !== undefined) headers['Content-Type'] = 'application/json'; if (options.origin) headers.Origin = options.origin; if (options.token) headers.Authorization = 'Bearer ' + options.token;
    const response = await fetch(base + route, {...options, headers, body: options.body === undefined ? undefined : JSON.stringify(options.body)});
    const text = await response.text(); return {status: response.status, body: text ? JSON.parse(text) : null};
  };
  const system = await json('/v1/system');
  if (!system.body.setupRequired) throw new Error('disposable server was not fresh');
  const token = await json('/v1/setup/browser', {method: 'POST', body: {}, origin: base});
  if (token.status !== 200) throw new Error('setup token refused: ' + JSON.stringify(token.body));
  const created = await json('/v1/setup', {method: 'POST', origin: base, body: {requestId: randomUUID(), setupToken: token.body.setupToken,
    username: credentials.username, password: credentials.password, name: 'Portico demo', authMode: 'local', interactive: true}});
  if (created.status !== 201) throw new Error('owner creation refused: ' + JSON.stringify(created.body));
  note('setup ok: local owner, name "Portico demo"');
  // The frame's top bar is data-derived. Create the same five empty libraries so
  // the shell's drawn destinations exist; no scan or media is claimed.
  for (const [kind, name] of [['movie', 'Movies'], ['tv', 'TV Shows'], ['anime', 'Anime'], ['music', 'Music'], ['audiobook', 'Audiobooks']]) {
    const directory = join(media, kind); await mkdir(directory, {recursive: true});
    const response = await fetch(base + '/v1/libraries', {method: 'POST', headers: {Authorization: 'Bearer ' + created.body.accessToken, 'Content-Type': 'application/json'},
      body: JSON.stringify({kind, name, path: directory, metadataAgent: 'local'})});
    if (response.status !== 201) note(`library ${name} refused: ${response.status} ${(await response.text()).slice(0, 160)}`);
  }

  browser = spawn(chromium, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu', '--disable-background-networking',
    '--disable-component-update', '--disable-sync', '--disable-default-apps', '--no-first-run', '--no-default-browser-check', '--remote-allow-origins=*',
    '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost, EXCLUDE 127.0.0.1, EXCLUDE fonts.googleapis.com, EXCLUDE fonts.gstatic.com', '--remote-debugging-address=127.0.0.1',
    `--remote-debugging-port=${chromePort}`, `--user-data-dir=${profile}`, 'about:blank'], {stdio: ['ignore', 'ignore', 'pipe']});
  let chromeLog = ''; browser.stderr.on('data', bytes => {chromeLog += bytes;});
  browser.on('exit', code => {note('chromium exited ' + code + '\n' + chromeLog.split('\n').slice(-8).join('\n'));});
  driver = await Driver.connect(chromePort);
  await driver.send('Page.enable'); await driver.send('Runtime.enable');
  await driver.viewport(1440, 1000);
  // The approved mock scales its 1440 stage to the document client width. A
  // classic scrollbar would shrink that to 1425 and make the pair incomparable;
  // hide scrollbars so both sides use the same 1440 logical layout.
  await driver.send('Emulation.setScrollbarsHidden', {hidden: true});
  await driver.navigate(base);
  await driver.waitFor(`document.querySelector('input[autocomplete="username"]')`, 30000);
  const setValue = async (selector, value) => driver.evaluate(`(() => {const element = document.querySelector(${JSON.stringify(selector)});
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; setter.call(element, ${JSON.stringify(value)});
    element.dispatchEvent(new Event('input', {bubbles: true})); return true;})()`);
  await setValue('input[autocomplete="username"]', credentials.username);
  await setValue('input[type="password"]', credentials.password);
  await driver.evaluate(`document.querySelector('button[type="submit"]').click()`);
  await driver.waitFor(`!document.querySelector('input[autocomplete="username"]')`, 30000);
  // One profile may be admitted directly; otherwise its picker tile carries the profile name.
  await driver.waitFor(`document.querySelector('nav[aria-label="Main"]') || [...document.querySelectorAll('button')].some(button => button.textContent.trim().startsWith(${JSON.stringify(credentials.username)}))`, 30000);
  if (!(await driver.evaluate(`!!document.querySelector('nav[aria-label="Main"]')`))) {
    await driver.evaluate(`[...document.querySelectorAll('button')].find(button => button.textContent.trim().startsWith(${JSON.stringify(credentials.username)})).click()`);
  }
  await driver.waitFor(`document.querySelector('nav[aria-label="Main"]')`, 30000);
  await driver.navigate(`${base}/settings?scope=server&section=remote-sources`);
  await driver.waitFor(`[...document.querySelectorAll('main h1')].some(heading => heading.textContent === 'Remote sources')`, 30000);
  await driver.waitFor(`[...document.querySelectorAll('main')].some(main => main.innerText.includes('No remote sources yet'))`, 30000);
  await driver.waitFor(`[...document.querySelectorAll('main')].some(main => main.innerText.includes('Add remote source'))`, 30000);
  await sleep(350);
  await driver.evaluate(`document.fonts.ready`);
  await driver.evaluate(`new Promise(resolve => {const settled = document.getAnimations().map(animation => animation.finished.catch(() => {})); Promise.allSettled(settled).then(() => requestAnimationFrame(() => requestAnimationFrame(resolve)));})`);
  await sleep(350);
  const buildShot = resolve(output, 'build.png');
  await driver.screenshot(buildShot);
  await driver.evaluate(`[...document.querySelectorAll('button')].find(button => button.textContent?.includes('Add remote source'))?.click()`);
  await driver.waitFor(`document.querySelector('[role="dialog"]')?.innerText.includes('Test connection')`, 15000);
  const addFacts = await driver.evaluate(`(() => {const dialog = document.querySelector('[role="dialog"]');return {open: !!dialog, text: dialog?.innerText.slice(0, 1200), fields: dialog?.querySelectorAll('input').length ?? 0};})()`);
  await driver.screenshot(resolve(output, 'add.png'));
  note(`Add dialog opened: ${JSON.stringify(addFacts)}`);
  await driver.evaluate(`document.querySelector('[role="dialog"] button[aria-label]')?.click()`);
  await sleep(250);
  const buildFacts = await driver.evaluate(`(() => {
    const box = element => {const r = element.getBoundingClientRect(); return {x: Math.round(r.x * 100) / 100, y: Math.round(r.y * 100) / 100, w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100};};
    const style = element => {const s = getComputedStyle(element); return {fontSize: s.fontSize, lineHeight: s.lineHeight, fontFamily: s.fontFamily, fontWeight: s.fontWeight, color: s.color};};
    return {url: location.href, viewport: {width: innerWidth, height: innerHeight, scrollHeight: document.documentElement.scrollHeight},
      headings: [...document.querySelectorAll('main h1, main h2, main h3')].map(element => ({text: element.textContent, box: box(element), style: style(element)})),
      groups: [...document.querySelectorAll('main section')].map(element => ({title: element.querySelector('h3')?.textContent ?? '', box: box(element)})),
      rows: [...document.querySelectorAll('main section')].map(element => ({id: element.querySelector('h3')?.textContent ?? '', text: element.innerText.replace(/\\n+/g, ' | '), box: box(element)})),
      nav: [...document.querySelectorAll('nav[aria-label] button')].map(element => ({text: element.textContent.trim(), current: element.getAttribute('aria-current'), box: box(element)})),
      saved: [...document.querySelectorAll('[role="status"]')].map(element => element.textContent.trim()).filter(Boolean),
      bodyText: document.body.innerText.split('\\n').map(line => line.trim()).filter(Boolean)};
  })()`);
  note(`build captured: ${buildFacts.headings.map(heading => heading.text).join(' / ')}`);

  // Approved read-only mock at the same viewport and state.
  await driver.navigate(`http://127.0.0.1:${designPort}/settings.html?demo=clean#server/remote-sources`);
  await driver.waitFor(`document.querySelector('#page .pg-h h2')?.textContent === 'Remote sources'`, 30000);
  await driver.waitFor(`document.querySelector('#page')?.innerText.includes('Add remote source')`, 30000);
  await driver.evaluate(`document.fonts.ready`);
  await sleep(450);
  await driver.evaluate(`new Promise(resolve => {const settled = document.getAnimations().map(animation => animation.finished.catch(() => {})); Promise.allSettled(settled).then(() => requestAnimationFrame(() => requestAnimationFrame(resolve)));})`);
  await sleep(300);
  const referenceShot = resolve(output, 'reference.png');
  await driver.screenshot(referenceShot);
  const referenceFacts = await driver.evaluate(`(() => {
    const box = element => {const r = element.getBoundingClientRect(); return {x: Math.round(r.x * 100) / 100, y: Math.round(r.y * 100) / 100, w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100};};
    const style = element => {const s = getComputedStyle(element); return {fontSize: s.fontSize, lineHeight: s.lineHeight, fontFamily: s.fontFamily, fontWeight: s.fontWeight, color: s.color};};
    return {url: location.href, viewport: {width: innerWidth, height: innerHeight, scrollHeight: document.documentElement.scrollHeight},
      headings: [...document.querySelectorAll('#page .pg-h h2, #page .sec-h h3')].map(element => ({text: element.textContent, box: box(element), style: style(element)})),
      groups: [...document.querySelectorAll('#page .sec')].map(element => ({title: element.querySelector('h3')?.textContent ?? '', box: box(element)})),
      rows: [...document.querySelectorAll('#page .r')].map(element => ({text: element.innerText.replace(/\\n+/g, ' | '), box: box(element)})),
      nav: [...document.querySelectorAll('#aside .it')].map(element => ({text: element.textContent.trim(), current: element.getAttribute('aria-current'), box: box(element)})),
      saved: [...document.querySelectorAll('.saved')].map(element => element.textContent.trim()).filter(Boolean),
      bodyText: document.body.innerText.split('\\n').map(line => line.trim()).filter(Boolean)};
  })()`);
  note(`reference captured: ${referenceFacts.headings.map(heading => heading.text).join(' / ')}`);

  // Raster difference for the human pair; anti-aliasing is reported, not chased.
  const buildBytes = (await readFile(buildShot)).toString('base64'), referenceBytes = (await readFile(referenceShot)).toString('base64');
  const diff = await driver.evaluate(`(async () => {
    const load = source => new Promise((resolve, reject) => {const image = new Image(); image.onload = () => resolve(image); image.onerror = reject; image.src = 'data:image/png;base64,' + source;});
    const [left, right] = await Promise.all([load(${JSON.stringify(buildBytes)}), load(${JSON.stringify(referenceBytes)})]);
    const canvas = document.createElement('canvas'); canvas.width = 1440; canvas.height = 1000; const context = canvas.getContext('2d', {willReadFrequently: true});
    context.drawImage(left, 0, 0); const a = context.getImageData(0, 0, 1440, 1000).data;
    context.clearRect(0, 0, 1440, 1000); context.drawImage(right, 0, 0); const b = context.getImageData(0, 0, 1440, 1000).data;
    let changed = 0, strong = 0, minX = 1440, minY = 1000, maxX = 0, maxY = 0; const bands = {};
    for (let y = 0; y < 1000; y++) for (let x = 0; x < 1440; x++) {
      const i = (y * 1440 + x) * 4; const delta = Math.max(Math.abs(a[i]-b[i]), Math.abs(a[i+1]-b[i+1]), Math.abs(a[i+2]-b[i+2]), Math.abs(a[i+3]-b[i+3]));
      if (delta > 8) {changed++; if (delta > 40) strong++; if (x < minX) minX = x; if (x > maxX) maxX = x; if (y < minY) minY = y; if (y > maxY) maxY = y;
        const band = Math.floor(y / 50) * 50; bands[band] = (bands[band] || 0) + 1;}
    }
    const pair = document.createElement('canvas'); pair.width = 2880; pair.height = 1000; const p = pair.getContext('2d'); p.fillStyle = '#000'; p.fillRect(0, 0, 2880, 1000);
    p.drawImage(left, 0, 0); p.drawImage(right, 1440, 0);
    return {format: pair.toDataURL('image/jpeg', 0.9), changed, strong, percent: Math.round(changed / 14400) / 100,
      bounds: changed ? {minX, minY, maxX, maxY} : null, bands: Object.entries(bands).sort((a, b) => Number(a[0]) - Number(b[0])).map(([y, count]) => ({y: Number(y), count}))};
  })()`);
  const pairBytes = Buffer.from(diff.format.split(',')[1], 'base64');
  await writeFile(resolve(output, 'pair.jpg'), pairBytes);
  const summary = {state: 'server/remote-sources, real empty state; Add dialog captured separately', viewport: {width: 1440, height: 1000},
    build: buildFacts, reference: referenceFacts, raster: {changed: diff.changed, strong: diff.strong, percent: diff.percent, bounds: diff.bounds, bands: diff.bands}};
  await writeFile(resolve(output, 'facts.log'), JSON.stringify(summary, null, 2));
  note(`raster changed=${diff.changed} (${diff.percent}%) strong=${diff.strong} bounds=${JSON.stringify(diff.bounds)}`);
  note('done');
} catch (error) {
  failure = error;
  try {note('failure url ' + await driver.evaluate('location.href'));} catch {}
  try {note('failure text ' + (await driver.evaluate('document.body.innerText')).slice(0, 2400).replace(/\n+/g, ' | '));} catch {}
  throw error;
} finally {
  try {driver?.socket.close();} catch {}
  try {browser?.kill('SIGTERM');} catch {}
  if (browser) await Promise.race([new Promise(resolve => browser.once('exit', resolve)), sleep(3000)]).catch(() => {});
  try {app?.kill('SIGTERM');} catch {}
  if (app) await Promise.race([new Promise(resolve => app.once('exit', resolve)), sleep(3000)]).catch(() => {});
  try {reference?.close();} catch {}
  try {dav?.close();} catch {}
  await rm(state, {recursive: true, force: true, maxRetries: 5, retryDelay: 200}).catch(() => {});
  await rm(media, {recursive: true, force: true, maxRetries: 5, retryDelay: 200}).catch(() => {});
  await rm(profile, {recursive: true, force: true, maxRetries: 5, retryDelay: 200}).catch(() => {});
  await writeFile(resolve(output, 'capture.log'), log.join('\n') + '\n').catch(() => {});
  if (failure) process.exitCode = 1;
}
