// auth1 / auth1c / auth3 lift-and-compare. Remote-only, no product data.
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {join, resolve} from 'node:path';
import {readFile} from 'node:fs/promises';
import {createServer} from 'vite';

const output = process.env.PORTICO_RUNNER_OUTPUT, design = process.env.PORTICO_RUNNER_SECONDARY_SOURCE;
if (!output || !design) throw new Error('Run on the external runner with --browser and the exact design secondary source.');
const version = (await readFile('.node-version', 'utf8')).trim();
// The capture is a display-only harness; the runner's Node 22 is enough for Vite.
assert.match(process.version, /^v(22|24)\./, `capture needs Node 22 or 24 (repo declares ${version})`);
const require = createRequire(join(process.env.PORTICO_BROWSER_TOOLS_DIR, 'loader.cjs'));
const {chromium} = require('playwright-core');
process.env.PORTICO_DEV_SERVER_URL = 'http://127.0.0.1:1';
const vite = await createServer({root: resolve('web'), configFile: resolve('web/vite.config.ts'),
  cacheDir: join(process.env.TMPDIR, 'setup-vite'), server: {host: '127.0.0.1', port: 0, strictPort: false}});
const source = await readFile(`${design}/design/ui-v1/auth.html`, 'utf8');
const kit = await readFile(`${design}/design/ui-v1/kit.css`, 'utf8');
const css = source.match(/<style>([\s\S]*?)<\/style>/)[1];
const sprite = [...source.matchAll(/<svg width="0" height="0" style="position:absolute">[\s\S]*?<\/svg>/g)].map(match => match[0]).join('');
const frame = id => {
  const start = source.indexOf(`<div class="frame" data-f="${id}">`);
  if (start < 0) throw new Error(`missing frame ${id}`);
  const end = source.indexOf('<div class="frame"', start + 10);
  return source.slice(start, end < 0 ? source.indexOf('<p class="cap"', start) : end)
    .replace('<div class="frame" data-f="' + id + '">', '')
    .replace(/\s*<span class="ptag">Proposed<\/span>/, '')
    .replace(/\s*<div class="bw"[\s\S]*?<\/div>/, '');
};
const wordmark = await readFile(`${design}/design/ui-v1/assets/wordmark.svg`).then(bytes => `data:image/svg+xml;base64,${bytes.toString('base64')}`);
const referenceHtml = id => `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/src/kit/fonts.css">
  <style>${kit}\n${css}\nhtml,body{margin:0;padding:0;background:#071018;overflow:hidden}\nbody .pg{inset:0}</style></head><body class="p">${sprite}${frame(id).replaceAll('assets/wordmark.svg', wordmark)}</body></html>`;

let browser;
const shots = [];
try {
  await vite.listen();
  const origin = `http://127.0.0.1:${vite.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, headless: true,
    args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--renderer-process-limit=1']});
  const page = await browser.newPage({viewport: {width: 1440, height: 860}, deviceScaleFactor: 1});
  const capture = async (name, viewport, id, prepare) => {
    await page.setViewportSize(viewport);
    await page.goto(`${origin}/dev/setup/setup.html${id === '3' ? '?frame=3' : id === '2b' || id === '2c' ? `?approval=${id === '2c' ? 'approved' : 'pending'}` : ''}`, {waitUntil: 'networkidle'});
    if (prepare) await prepare();
    await page.evaluate(async () => {await document.fonts.ready; await Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => {})));});
    await page.mouse.move(1, 1);
    await page.screenshot({path: join(output, `${name}-build.jpg`), type: 'jpeg', quality: 94});
    await page.route('**/__setup-reference__', route => route.fulfill({contentType: 'text/html', body: referenceHtml(id)}));
    await page.goto(`${origin}/__setup-reference__`, {waitUntil: 'networkidle'});
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({path: join(output, `${name}-frame.jpg`), type: 'jpeg', quality: 94});
    const build = await page.evaluate(() => [...document.querySelectorAll('h1,p,label,button,[role="radio"],[role="group"],small')].map(element => ({tag: element.tagName, text: element.textContent, box: element.getBoundingClientRect().toJSON(), font: getComputedStyle(element).font})));
    shots.push({name, build});
    const a = await readFile(join(output, `${name}-build.jpg`)), b = await readFile(join(output, `${name}-frame.jpg`));
    await page.setViewportSize({width: viewport.width * 2, height: viewport.height});
    await page.setContent(`<body style="margin:0;display:flex;background:#111"><img width="${viewport.width}" src="data:image/jpeg;base64,${a.toString('base64')}"><img width="${viewport.width}" src="data:image/jpeg;base64,${b.toString('base64')}"></body>`);
    await page.screenshot({path: join(output, `${name}-pair.jpg`), type: 'jpeg', quality: 92});
    await page.setViewportSize(viewport);
  };
  await capture('setup-1', {width: 1440, height: 860}, '1');
  await capture('setup-1c', {width: 1440, height: 1130}, '1c', async () => {
    await page.getByRole('radio', {name: /Local sign-in/}).click();
    await page.getByLabel('Username', {exact: true}).fill('demo');
    await page.getByLabel('Password', {exact: true}).fill('Portico-1234!x');
    await page.getByLabel('Confirm password', {exact: true}).fill('Portico-1234!x');
    await page.getByLabel('Confirm password', {exact: true}).focus();
  });
  await capture('setup-3', {width: 1440, height: 1120}, '3', async () => {
    await page.getByLabel('Or type a path', {exact: true}).fill('/srv/media/Movies');
  });
  await capture('setup-2b', {width: 1440, height: 986}, '2b');
  await capture('setup-2c', {width: 1440, height: 860}, '2c');
  await (await import('node:fs/promises')).writeFile(join(output, 'setup-capture.json'), JSON.stringify(shots, null, 2));
  console.log(JSON.stringify(shots.map(shot => shot.name)));
} finally {
  await browser?.close();
  await vite.close();
}
