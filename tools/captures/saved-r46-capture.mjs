// Same-size, same-state capture of the Saved screen and its frame. Run on the
// external runner with --browser and the exact design secondary source. It
// mounts the genuine screen fixture and the frame's own markup/CSS; it commits
// no frame, artwork or private input. Diagnostic only: this is not the fidelity
// comparator, and its pixel metric is a plain difference for the lane report.
import {createRequire} from 'node:module';
import {execFileSync} from 'node:child_process';
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

const viewport = {width: 1440, height: 900};
const state = process.env.PORTICO_SAVED_STATE ?? 'view=watchlist&sort=added&direction=desc';
const scope = process.env.PORTICO_SAVED_SCOPE ?? 'saved';
const mime = {'.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.png': 'image/png', '.svg': 'image/svg+xml', '.webp': 'image/webp'};

// Serves only the design's pool artwork to the two controlled pages. Nothing is
// copied or committed. A plugin's configureServer hook registers before Vite's
// own static/SPA middlewares, so the frame's relative asset paths resolve.
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
const server = await createServer({root, configFile: path.join(root, 'vite.config.ts'), cacheDir: path.join(process.env.TMPDIR ?? output, 'saved-r46-vite'),
  plugins: [{name: 'ds-web-saved-r46-design-assets', configureServer(context) { context.middlewares.use(assetMiddleware); }}],
  server: {host: '127.0.0.1', port: 0, strictPort: false}});

const gridSelectors = {
  screen: '#saved', title: '#saved .phd h1', tabs: '#saved .tabs', tabSelected: '#saved .tab[aria-selected="true"]',
  toolbar: '#saved .tb', filters: '#saved .tb > .chip:nth-child(1)', quick: '#saved .tb > .chip:nth-child(3)',
  count: '#saved .tb .r', sort: '#saved .tb .r > .chip', segmented: '#saved .tb .seg',
  grid: '#saved .grid', card1: '#saved .grid .card:nth-child(1)', art1: '#saved .grid .card:nth-child(1) .art',
  cardTitle1: '#saved .grid .card:nth-child(1) .t', cardSub1: '#saved .grid .card:nth-child(1) .s',
  card8: '#saved .grid .card:nth-child(8)', art8: '#saved .grid .card:nth-child(8) .art',
  section: '#saved .sub2', sectionHeading: '#saved .sub2 .sh', squareArt: '#saved .sub2 .card .art',
};
const historySelectors = {
  screen: '#history', title: '#history .phd h1', tabs: '#history .tabs', tabSelected: '#history .tab[aria-selected="true"]',
  toolbar: '#history .tb', filters: '#history .tb > .chip:nth-child(1)', quick: '#history .tb > .chip:nth-child(3)',
  count: '#history .tb .r', sort: '#history .tb .r > .chip', segmented: '#history .tb .seg',
  grid: '#history .day:nth-of-type(1)', card1: '#history .day:nth-of-type(1) .hl:nth-of-type(1)', art1: '#history .day:nth-of-type(1) .hl:nth-of-type(1) .a',
  cardTitle1: '#history .day:nth-of-type(1) .hl:nth-of-type(1) b', cardSub1: '#history .day:nth-of-type(1) .hl:nth-of-type(1) small',
  card8: null, art8: null, section: '#history .day:nth-of-type(2)', sectionHeading: '#history .day:nth-of-type(2) h3', squareArt: '#history .day:nth-of-type(1) .hl:nth-of-type(2) .a',
};
const activeSelectors = scope === 'history' ? historySelectors : gridSelectors;
const buildHistoryMeasure = () => {
  const query = selector => document.querySelector(selector);
  const byText = (selector, text) => [...document.querySelectorAll(selector)].find(element => element.textContent.trim() === text);
  const days = [...document.querySelectorAll('main [class*="_historyDay_"]')], rows = [...document.querySelectorAll('main [class*="_historyRow_"]')];
  const elements = {
    screen: query('main'), title: query('main > header h1'), tabs: query('main [role="tablist"]'), tabSelected: query('main [role="tab"][aria-selected="true"]'),
    toolbar: query('main [class*="_toolbar_"]'), filters: null, quick: byText('main button', 'Today'),
    count: query('main [class*="_toolbarEnd_"]'), sort: null, segmented: null,
    grid: days[0], card1: rows[0], art1: rows[0]?.querySelector('[class*="_historyArt_"]'),
    cardTitle1: rows[0]?.querySelector('b'), cardSub1: rows[0]?.querySelector('small'),
    card8: null, art8: null, section: days[1], sectionHeading: days[1]?.querySelector('h2'), squareArt: rows[1]?.querySelector('[class*="_historyArt_"]'),
  };
  const box = element => {
    const rect = element.getBoundingClientRect(), style = getComputedStyle(element);
    return {x: +rect.x.toFixed(2), y: +rect.y.toFixed(2), width: +rect.width.toFixed(2), height: +rect.height.toFixed(2),
      font: style.font, letterSpacing: style.letterSpacing, color: style.color, background: style.backgroundColor, radius: style.borderRadius};
  };
  return Object.fromEntries(Object.entries(elements).map(([name, element]) => [name, element ? box(element) : null]));
};
const buildMeasure = () => {
  const query = selector => document.querySelector(selector);
  const byText = (selector, text) => [...document.querySelectorAll(selector)].find(element => element.textContent.trim() === text);
  const sections = [...document.querySelectorAll('main section[class*="_savedSection_"]')], articles = [...document.querySelectorAll('main article[data-card-kind]')];
  const art = element => element && element.querySelector('[class*="_art_"]');
  const elements = {
    screen: query('main'), title: query('main > header h1'), tabs: query('main [role="tablist"]'), tabSelected: query('main [role="tab"][aria-selected="true"]'),
    toolbar: query('main [class*="_toolbar_"]'), filters: byText('main button', 'Filters'), quick: byText('main button', 'Unwatched'),
    count: query('main [class*="_toolbarEnd_"]'), sort: [...document.querySelectorAll('main button')].find(element => element.textContent.includes('Date added')),
    segmented: query('main [class*="_viewToggle_"]'), grid: sections[0], card1: articles[0], art1: art(articles[0]),
    cardTitle1: articles[0]?.querySelector('[class*="_title_"]'), cardSub1: articles[0]?.querySelector('[class*="_subtitle_"]'),
    card8: articles[7], art8: art(articles[7]), section: sections[1], sectionHeading: query('main [class*="_sectionHeading_"]'),
    squareArt: art(sections[1]?.querySelector('article')),
  };
  const box = element => {
    const rect = element.getBoundingClientRect(), style = getComputedStyle(element);
    return {x: +rect.x.toFixed(2), y: +rect.y.toFixed(2), width: +rect.width.toFixed(2), height: +rect.height.toFixed(2),
      font: style.font, letterSpacing: style.letterSpacing, color: style.color, background: style.backgroundColor, radius: style.borderRadius};
  };
  return Object.fromEntries(Object.entries(elements).map(([name, element]) => [name, element ? box(element) : null]));
};

let browser;
const report = {version: 1, scope, state, viewport, generatedAt: new Date().toISOString(), reference: {}, build: {}, files: {}, differences: [], pixel: null, errors: [], assets: [], artwork: []};
try {
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath, headless: true, args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--renderer-process-limit=1']});
  const context = await browser.newContext({viewport, deviceScaleFactor: 1, colorScheme: 'dark', reducedMotion: 'reduce', locale: 'en-US', timezoneId: 'UTC'});
  const page = await context.newPage();
  page.on('pageerror', error => report.errors.push('pageerror: ' + error.message));
  page.on('console', message => { if (message.type() === 'error') report.errors.push('console: ' + message.text()); });
  page.on('requestfailed', request => report.errors.push('requestfailed: ' + request.url().replace(/^https?:\/\/[^/]+/, '') + ' ' + (request.failure()?.errorText ?? '')));
  page.on('response', response => { const url = response.url(); if (url.includes('/__design-assets__/') || /\/assets\//.test(url)) report.assets.push({path: url.replace(/^https?:\/\/[^/]+/, ''), status: response.status()}); });

  const settle = async (selector, minimum, tag) => {
    await page.evaluate(() => document.fonts.ready);
    try {
      await page.waitForFunction(([selector, count]) => document.querySelectorAll(selector).length >= count, [selector, minimum], {timeout: 20000});
      await page.waitForFunction(() => [...document.images].every(image => image.complete), null, {timeout: 20000});
      await page.waitForFunction(() => [...document.querySelectorAll('[data-artwork-status]')].every(element => ['ready', 'error'].includes(element.dataset.artworkStatus)), null, {timeout: 20000});
    } catch (error) {
      await page.screenshot({path: `${output}/saved-${tag}-failure.png`, fullPage: true}).catch(() => {});
      throw new Error(`${tag} did not settle: ${error.message}; text=${JSON.stringify((await page.evaluate(() => document.body.innerText)).slice(0, 400))}; fixture=${JSON.stringify(await page.evaluate(() => { const fixture = window.__savedFixture; return fixture ? {posters: fixture.runtime.posters.getSnapshot().error, squares: fixture.runtime.squares.getSnapshot().error, personal: fixture.runtime.personal.getSnapshot().error, route: fixture.runtime.posters.getSnapshot().route} : 'none'; }))}; errors=${JSON.stringify(report.errors.slice(0, 6))}`);
    }
    await page.waitForTimeout(400);
  };
  const measure = async (selectors) => page.evaluate((list) => {
    const box = element => {
      const rect = element.getBoundingClientRect(), style = getComputedStyle(element);
      return {x: +rect.x.toFixed(2), y: +rect.y.toFixed(2), width: +rect.width.toFixed(2), height: +rect.height.toFixed(2),
        font: style.font, letterSpacing: style.letterSpacing, color: style.color, background: style.backgroundColor, radius: style.borderRadius};
    };
    return Object.fromEntries(Object.entries(list).map(([name, selector]) => {
      const element = selector ? document.querySelector(selector) : null;
      return [name, element ? box(element) : null];
    }));
  }, selectors);

  // The genuine screen over its controlled source fixture.
  await page.goto(`${origin}/src/screens/saved/ds-web-saved-r46-preview.html?${state}`, {waitUntil: 'domcontentloaded'});
  await settle(scope === 'history' ? '[class*="_historyRow_"]' : 'article[data-card-kind]', scope === 'history' ? 7 : 14, 'build');
  report.build = await page.evaluate(scope === 'history' ? buildHistoryMeasure : buildMeasure);
  report.artwork = await page.evaluate(() => [...document.querySelectorAll('[data-artwork-status]')].map(element => element.dataset.artworkStatus));
  report.buildText = (await page.evaluate(() => document.body.innerText)).split('\n').map(line => line.trim()).filter(Boolean);
  report.files.build = 'saved-build.png';
  await page.screenshot({path: `${output}/saved-build.png`, fullPage: true});

  // The frame's own markup and CSS, served in the browser; nav is hidden because
  // the top bar belongs to the shell, which this screen does not own.
  const source = await readFile(path.join(designRoot, 'desktop-search.html'), 'utf8');
  const kit = await readFile(path.join(designRoot, 'kit.css'), 'utf8');
  const css = source.match(/<style>([\s\S]*?)<\/style>/)[1];
  const frameName = scope === 'history' ? 'history' : 'saved';
  const start = source.indexOf(`<div class="screen" id="${frameName}"`);
  const end = source.indexOf(scope === 'history' ? '<p class="cap"><b>Avatar menu' : '<p class="cap"><b>Saved, History', start);
  if (start < 0 || end < 0) throw new Error(`${frameName} frame not found in the design source.`);
  const sprite = [...source.matchAll(/<svg width="0" height="0" style="position:absolute">[\s\S]*?<\/svg>/g)].map(match => match[0]).join('');
  const referenceHtml = `<!doctype html><html lang="en"><head><meta charset="utf-8"><link rel="stylesheet" href="${origin}/src/kit/fonts.css"><style>${kit}\n${css}\nhtml,body{margin:0;background:#071018}.screen{overflow:visible}nav{visibility:hidden}</style></head><body class="p">${sprite}${source.slice(start, end)}</body></html>`;
  await page.route('**/__saved_reference__', route => route.fulfill({contentType: 'text/html', body: referenceHtml}));
  await page.goto(`${origin}/__saved_reference__`, {waitUntil: 'domcontentloaded'});
  await settle(scope === 'history' ? '#history .day .hl' : '#saved .grid .card', scope === 'history' ? 7 : 14, 'frame');
  report.reference = await measure(activeSelectors);
  report.referenceText = (await page.evaluate(() => document.body.innerText)).split('\n').map(line => line.trim()).filter(Boolean);
  report.files.reference = 'saved-frame.png';
  await page.screenshot({path: `${output}/saved-frame.png`, fullPage: true});

  // The same-state pair, side by side.
  const [frameBytes, buildBytes] = await Promise.all([readFile(`${output}/saved-frame.png`), readFile(`${output}/saved-build.png`)]);
  await page.setViewportSize({width: 2880, height: 900});
  await page.setContent(`<body style="margin:0;display:flex;align-items:flex-start;background:#03080c"><img width="1440" src="data:image/png;base64,${frameBytes.toString('base64')}"><img width="1440" src="data:image/png;base64,${buildBytes.toString('base64')}"></body>`);
  await page.screenshot({path: `${output}/saved-comparison.jpg`, type: 'jpeg', quality: 90, fullPage: true});
  report.files.comparison = 'saved-comparison.jpg';

  // A plain difference, only when the two capture heights allow it. This is a
  // diagnostic number, never a fidelity verdict.
  try {
    const tool = (() => { try { execFileSync('compare', ['-version'], {stdio: 'ignore'}); return 'compare'; } catch { return 'magick'; } })();
    const run = (args, options = {}) => tool === 'magick' ? execFileSync('magick', [args[0] === 'compare' ? 'compare' : args[0], ...args.slice(1)], {encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], ...options}) : execFileSync(args[0], args.slice(1), {encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], ...options});
    const size = file => run(['identify', '-format', '%w %h', `${output}/${file}`]).trim().split(' ').map(Number);
    const frameSize = size('saved-frame.png'), buildSize = size('saved-build.png'), height = Math.min(frameSize[1], buildSize[1]);
    run(['convert', `${output}/saved-frame.png`, '-crop', `1440x${height}+0+0`, '+repage', `${output}/saved-frame-crop.png`]);
    run(['convert', `${output}/saved-build.png`, '-crop', `1440x${height}+0+0`, '+repage', `${output}/saved-build-crop.png`]);
    let metric = '';
    try { run(['compare', '-metric', 'AE', '-fuzz', '2%', `${output}/saved-frame-crop.png`, `${output}/saved-build-crop.png`, `${output}/saved-diff.png`]); }
    catch (error) { metric = String(error.stderr ?? '').trim(); }
    const count = Number.parseFloat(metric.replace(/[^0-9.eE+-]/g, '')), pixels = height * 1440;
    report.pixel = {height, differingPixels: Number.isFinite(count) ? count : null, percent: Number.isFinite(count) ? +(100 * count / pixels).toFixed(3) : null, note: 'Plain AE with 2% fuzz, cropped to the shared height; diagnostic only.'};
    report.files.diff = 'saved-diff.png';
  } catch (error) { report.pixel = {note: 'ImageMagick unavailable or crops differ: ' + String(error.message).slice(0, 200)}; }

  for (const [name, reference] of Object.entries(report.reference)) {
    const build = report.build[name];
    if (reference && build) report.differences.push({name, dx: +(build.x - reference.x).toFixed(2), dy: +(build.y - reference.y).toFixed(2), dw: +(build.width - reference.width).toFixed(2), dh: +(build.height - reference.height).toFixed(2)});
  }
  await writeFile(`${output}/saved-capture.json`, JSON.stringify(report, null, 2) + '\n');
  const captured = {verdict: 'CAPTURED', scope, state, pixel: report.pixel, differences: report.differences, artwork: report.artwork, assets: report.assets.filter(asset => asset.status !== 200).slice(0, 8), assetCount: report.assets.length, errors: report.errors.slice(0, 6)};
  console.log('SAVED-CAPTURE ' + JSON.stringify(captured));
} finally {
  await browser?.close();
  await server.close();
}
