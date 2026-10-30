// Remote-only acceptance harness. No server data or credentials; one browser and server.
import {spawn} from 'node:child_process';
import {readFile, writeFile, symlink, unlink} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {checkSeriesRules} from './live-series-rules.browser.mjs';
const output = process.env.PORTICO_RUNNER_OUTPUT, reference = process.env.PORTICO_RUNNER_SECONDARY_SOURCE;
if (!output || !reference || !process.env.PORTICO_BROWSER_MODULE) throw new Error('Run on the external runner with --browser and the exact design secondary source.');
const {chromium} = await import(pathToFileURL(process.env.PORTICO_BROWSER_MODULE));
// Existing gallery families resolve the design checkout as a sibling worktree.
// The runner supplies that exact archive separately; link it only for this job.
const designLink = resolve('../../design-ui-v1');
await symlink(reference, designLink);
const origin = 'http://127.0.0.1:5188';
const server = spawn(process.execPath, [resolve('node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '5188'], {cwd: resolve('web'), detached: true, stdio: ['ignore', 'pipe', 'pipe']});
let log = '';
server.stdout.on('data', data => {log += data;});server.stderr.on('data', data => {log += data;});
let browser;
try {
  for (let attempt = 0; ; attempt++) {
    try {if ((await fetch(origin)).ok) break;} catch {}
    if (attempt > 100 || server.exitCode !== null) throw new Error(`Vite did not start: ${log}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  browser = await chromium.launch({executablePath: process.env.PORTICO_CHROMIUM ?? '/usr/bin/chromium', headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage']});
  const page = await browser.newPage({viewport: {width: 1440, height: 950}, deviceScaleFactor: 1});
  const result = await checkSeriesRules(page, origin);
  await page.goto(`${origin}/dev/kit?family=live-rules&case=editor&capture=true`);
  await page.locator('[data-gallery-ready=true]').waitFor();
  await page.getByRole('region', {name: 'Edit rule for The Clockmakers'}).waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(350);
  await page.evaluate(() => document.activeElement?.blur());
  const implementationImage = await page.screenshot({path: `${output}/implementation.jpg`, type: 'jpeg', quality: 90});
  const build = await page.evaluate(() => [...document.querySelectorAll('h1,[role=tablist],article,aside,aside h2,aside footer,aside h3')].map(element => ({text: element.textContent, box: element.getBoundingClientRect().toJSON()})));
  const source = await readFile(`${reference}/design/ui-v1/desktop-live.html`, 'utf8');
  const kit = await readFile(`${reference}/design/ui-v1/kit.css`, 'utf8');
  const css = source.match(/<style>([\s\S]*?)<\/style>/)[1];
  const frame = source.slice(source.indexOf('<div class="screen" id="live-rules"'), source.indexOf('<p class="cap"><b>Watching live'));
  const sprite = [...source.matchAll(/<svg width="0" height="0" style="position:absolute">[\s\S]*?<\/svg>/g)].map(match => match[0]).join('');
  const referenceHtml = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="${origin}/src/kit/fonts.css"><style>${kit}\n${css}\nbody{margin:0;padding:0}</style></head><body class="p">${sprite}${frame.replace('assets/wordmark.svg', origin + '/src/shell/wordmark.svg')}</body></html>`;
  await page.route('**/__series-rules-reference__', route => route.fulfill({contentType: 'text/html', body: referenceHtml}));
  await page.goto(`${origin}/__series-rules-reference__`);
  await page.evaluate(() => document.fonts.ready);
  const frameImage = await page.screenshot({path: `${output}/frame.jpg`, type: 'jpeg', quality: 90});
  const drawn = await page.evaluate(() => [...document.querySelectorAll('h1,.tabs,.rule,aside,.sh-title,.sh-foot,.sh-h')].map(element => ({text: element.textContent, box: element.getBoundingClientRect().toJSON()})));
  await page.setViewportSize({width: 2880, height: 950});
  await page.setContent(`<body style="margin:0;display:flex"><img width="1440" height="950" src="data:image/jpeg;base64,${implementationImage.toString('base64')}"><img width="1440" height="950" src="data:image/jpeg;base64,${frameImage.toString('base64')}"></body>`);
  await page.screenshot({path: `${output}/comparison.jpg`, type: 'jpeg', quality: 90});
  await writeFile(`${output}/browser.log`, JSON.stringify({result, build, drawn}, null, 2));
  console.log(JSON.stringify(result));
} finally {
  await browser?.close();
  try {process.kill(-server.pid, 'SIGTERM');} catch {}
  await new Promise(resolve => {if (server.exitCode !== null) resolve();else server.once('exit', resolve);});
  await writeFile(`${output}/vite.log`, log);
  await unlink(designLink);
}
