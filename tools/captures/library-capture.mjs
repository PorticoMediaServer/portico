// Same-size, same-state capture of the Library screen's drawn states and their
// desktop-browse.html frames. Run on the external runner with --browser and the
// exact design secondary source. It mounts the genuine DEV specimen and the
// frame's own markup/CSS; it commits no frame, artwork or private input.
// Diagnostic only: a plain side-by-side pair for the lane report, never a
// fidelity verdict and never a second comparator.
import {createRequire} from 'node:module';
import {readFile, writeFile, mkdir} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {createServer} from 'vite';
import path from 'node:path';

const output = process.env.PORTICO_RUNNER_OUTPUT, design = process.env.PORTICO_RUNNER_SECONDARY_SOURCE;
const executablePath = process.env.PORTICO_CHROMIUM ?? ['/usr/bin/chromium', '/snap/bin/chromium', '/usr/bin/google-chrome'].find(existsSync);
if (!executablePath) throw new Error('No Chromium executable on this host.');
if (!output || !design) throw new Error('Run with PORTICO_RUNNER_OUTPUT and the exact design PORTICO_RUNNER_SECONDARY_SOURCE.');
const root = process.env.PORTICO_RUNNER_SITE ?? path.resolve('.');
const designRoot = path.join(design, 'design/ui-v1');
const require = createRequire(import.meta.url);
const {chromium} = require('playwright-core');
await mkdir(output, {recursive: true});

// Every drawn state of the screen this lane owns, in frame order.
const states = ['grid', 'big', 'sort', 'filters', 'list', 'collections', 'empty'];
const mime = {'.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.png': 'image/png', '.svg': 'image/svg+xml', '.webp': 'image/webp'};

const assetMiddleware = async (request, response, next) => {
  const pathname = decodeURIComponent(new URL(request.url, 'http://127.0.0.1').pathname);
  const prefix = pathname.startsWith('/__design-assets__/assets/') ? '/__design-assets__/assets/' : pathname.startsWith('/assets/') ? '/assets/' : undefined;
  if (!prefix) return next();
  const relative = pathname.slice(prefix.length);
  if (!relative || relative.includes('..') || path.isAbsolute(relative)) return next();
  try {
    const body = await readFile(path.join(designRoot, 'assets', relative));
    response.setHeader('Content-Type', mime[path.extname(relative).toLowerCase()] ?? 'application/octet-stream');
    response.end(body);
  } catch { response.statusCode = 404; response.end(); }
};
const server = await createServer({root, configFile: path.join(root, 'vite.config.ts'), cacheDir: path.join(process.env.TMPDIR ?? output, 'library-capture-vite'),
  plugins: [{name: 'ds-web-library-design-assets', configureServer(context) { context.middlewares.use(assetMiddleware); }}],
  server: {host: '127.0.0.1', port: 0, strictPort: false}});

const source = await readFile(path.join(designRoot, 'desktop-browse.html'), 'utf8');
const kit = await readFile(path.join(designRoot, 'kit.css'), 'utf8');
const css = source.match(/<style>([\s\S]*?)<\/style>/)[1];
const sprite = [...source.matchAll(/<svg width="0" height="0" style="position:absolute">[\s\S]*?<\/svg>/g)].map(match => match[0]).join('');
const frameHtml = (origin, name) => {
  const start = source.indexOf(`<div class="screen" id="${name}"`);
  if (start < 0) throw new Error(`#${name} frame not found in the design source.`);
  const next = source.indexOf('<p class="cap"', start), script = source.indexOf('<script', start);
  const end = next >= 0 && (script < 0 || next < script) ? next : script >= 0 ? script : source.length;
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><link rel="stylesheet" href="${origin}/src/kit/fonts.css"><style>${kit}\n${css}\nhtml,body{margin:0;background:#071018}.screen{overflow:visible}</style></head><body class="p">${sprite}${source.slice(start, end)}</body></html>`;
};

let browser;
const report = {version: 1, screen: 'library', viewport: {width: 1440, height: 900}, states: {}, errors: [], assets: []};
try {
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath, headless: true, args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--renderer-process-limit=1']});
  const context = await browser.newContext({viewport: report.viewport, deviceScaleFactor: 1, colorScheme: 'dark', reducedMotion: 'reduce', locale: 'en-US', timezoneId: 'UTC'});
  const page = await context.newPage();
  page.setDefaultTimeout(15000); page.setDefaultNavigationTimeout(20000);
  // A starved shared VPS must not leave a capture hanging past its useful life.
  let watchdog;
  const arm = () => {clearTimeout(watchdog); watchdog = setTimeout(async () => {
    console.log('CAPTURE_WATCHDOG'); try {await browser?.close();} catch {} try {await server.close();} catch {} process.exit(1);}, 360000);};
  const step = async (label, work) => {arm(); const started = Date.now(); console.log('STEP', label); const value = await work(); console.log('STEP', label, 'ms', Date.now() - started); return value;};
  page.on('pageerror', error => report.errors.push('pageerror: ' + error.message));
  page.on('console', message => { if (message.type() === 'error') report.errors.push('console: ' + message.text()); });
  page.on('requestfailed', request => report.errors.push('requestfailed: ' + request.url().replace(/^https?:\/\/[^/]+/, '') + ' ' + (request.failure()?.errorText ?? '')));
  page.on('response', response => { const url = response.url(); if (url.includes('/__design-assets__/') || /\/assets\//.test(url)) report.assets.push({path: url.replace(/^https?:\/\/[^/]+/, ''), status: response.status()}); });

  const settle = async tag => {
    await page.evaluate(() => document.fonts.ready);
    await page.waitForFunction(() => [...document.images].every(image => image.complete), null, {timeout: 20000});
    await page.waitForFunction(() => [...document.querySelectorAll('[data-artwork-status]')].every(element => ['ready', 'error'].includes(element.dataset.artworkStatus)), null, {timeout: 20000});
    await page.waitForTimeout(500);
  };
  const side = async (state, framePath, buildPath) => {
    const [frameBytes, buildBytes] = await Promise.all([readFile(framePath), readFile(buildPath)]);
    await page.setViewportSize({width: 2880, height: 900});
    await page.setContent(`<body style="margin:0;display:flex;align-items:flex-start;background:#03080c"><img width="1440" src="data:image/png;base64,${frameBytes.toString('base64')}"><img width="1440" src="data:image/png;base64,${buildBytes.toString('base64')}"></body>`);
    const file = `library-${state}-comparison.jpg`;
    await page.screenshot({path: `${output}/${file}`, type: 'jpeg', quality: 90, fullPage: true});
    await page.setViewportSize(report.viewport);
    return file;
  };

  for (const state of states) {
    report.states[state] = {files: {}, errors: []};
    await step(`${state}:build`, async () => {
      await page.goto(`${origin}/src/dev/entries/library.html?state=${state}`, {waitUntil: 'domcontentloaded'});
      await page.waitForSelector('main', {timeout: 15000});
      await settle(`build:${state}`);
    });
    // Frames #sort and #filters draw their menu and panel open.
    if (state === 'sort') {
      const trigger = page.locator('button', {hasText: 'Newest first'}).first();
      if (await trigger.count()) { await trigger.click(); await page.waitForTimeout(400); }
    }
    // Frame #big is drawn scrolled: the header is gone and the toolbar sticks under the nav.
    if (state === 'big') { await page.evaluate(() => window.scrollTo(0, 280)); await page.waitForTimeout(400); }
    if (state === 'filters') {
      const filters = page.locator('button', {hasText: 'Filters'}).first();
      if (await filters.count()) { await filters.click(); await page.waitForTimeout(600); }
      // Frame #filters draws the Genre facet open with its values, counts and search box.
      const dialog = page.getByRole('dialog');
      const genre = dialog.locator('button', {hasText: 'Genre'}).first();
      if (await genre.count()) { await genre.click(); await page.waitForTimeout(1500); }
    }
    report.states[state].buildText = (await page.evaluate(() => document.body.innerText)).split('\n').map(line => line.trim()).filter(Boolean).slice(0, 60);
    report.states[state].files.build = `library-${state}-build.png`;
    await step(`${state}:build-shot`, () => page.screenshot({path: `${output}/library-${state}-build.png`, fullPage: state !== 'big'}));

    await step(`${state}:frame`, async () => {
      await page.route('**/__library_reference__', route => route.fulfill({contentType: 'text/html', body: frameHtml(origin, state === 'collections' ? 'collections' : state)}));
      await page.goto(`${origin}/__library_reference__`, {waitUntil: 'domcontentloaded'});
      await settle(`frame:${state}`);
    });
    report.states[state].files.reference = `library-${state}-frame.png`;
    await step(`${state}:frame-shot`, () => page.screenshot({path: `${output}/library-${state}-frame.png`, fullPage: true}));
    await page.unroute('**/__library_reference__');

    report.states[state].files.comparison = await step(`${state}:side`, () => side(state, `${output}/library-${state}-frame.png`, `${output}/library-${state}-build.png`));
  }
  clearTimeout(watchdog);
  await writeFile(`${output}/library-capture.json`, JSON.stringify(report, null, 2) + '\n');
  console.log('LIBRARY-CAPTURE ' + JSON.stringify({states, errors: report.errors.slice(0, 6), assets: report.assets.filter(asset => asset.status !== 200).slice(0, 8), assetCount: report.assets.length}));
} finally {
  await browser?.close();
  await server.close();
}
