// Reading verification on the live demo: for one book or comic, record what the reader actually
// fetched and whether the bytes decoded — status, MIME, length, magic, SHA-256, decode outcome.
// Never prints a grant URL, a query or a body.
//
//   SMOKE_CREDENTIALS=/path/creds.json SMOKE_OUT=/path/out \
//   node smoke/read/verify.mjs --title "1905"
//
// Magic alone is not evidence of a valid image; the createImageBitmap/decode outcome is what counts.
import {readFileSync, mkdirSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {loadPlaywright, launch, Evidence, settle, bodyText} from '../lib/page.mjs';

const origin = 'https://demo.getportico.tv';
const out = process.env.SMOKE_OUT || join(process.cwd(), 'read-verify');
const credsPath = process.env.SMOKE_CREDENTIALS || '';
const args = process.argv.slice(2);
const title = args[args.indexOf('--title') + 1] || '1905';
const library = args[args.indexOf('--library') + 1] || 'Comics';
mkdirSync(out, {recursive: true});
const creds = JSON.parse(readFileSync(credsPath, 'utf8'));

const playwright = await loadPlaywright();
const {browser, context, page} = await launch(playwright, {viewport: {width: 1440, height: 900}});
const evidence = new Evidence(out);

// Record every bitmap decode attempt the reader makes, before any app code runs.
await context.addInitScript(() => {
  window.__smokeBitmaps = [];
  const original = window.createImageBitmap;
  if (typeof original === 'function') {
    window.createImageBitmap = function patched(...callArgs) {
      const entry = {at: new Date().toISOString()};
      try {
        const promise = original.apply(this, callArgs);
        if (promise && typeof promise.then === 'function') {
          promise.then(bitmap => {
            entry.ok = true; entry.width = bitmap.width; entry.height = bitmap.height;
            window.__smokeBitmaps.push(entry);
          }, error => {
            entry.ok = false; entry.error = String(error && error.message ? error.message : error).slice(0, 160);
            window.__smokeBitmaps.push(entry);
          });
        }
        return promise;
      } catch (error) {
        entry.ok = false; entry.error = String(error).slice(0, 160);
        window.__smokeBitmaps.push(entry);
        throw error;
      }
    };
  }
});

const resources = [];
page.on('response', async response => {
  const url = new URL(response.url());
  const path = url.pathname;
  const isReaderResource = /^\/v1\/(reading|items\/[^/]+\/reading|media)/.test(path) || /\.(png|jpg|jpeg|gif|webp|avif|cbz|epub|pdf)$/i.test(path);
  if (!isReaderResource) return;
  const entry = {
    at: new Date().toISOString(), status: response.status(), method: response.request().method(),
    pathPrefix: path.split('/').map((segment, index, all) =>
      (all[index - 1] === 'sessions' && segment !== 'current') ? '{id}' : segment).join('/'),
    mime: response.headers()['content-type'] || null,
    length: response.headers()['content-length'] || null,
  };
  if (response.status() >= 400) {
    try {
      const body = await response.json();
      const code = body && body.error && typeof body.error.code === 'string' ? body.error.code.slice(0, 60) : undefined;
      if (code) entry.errorCode = code;
    } catch { /* a refusal with no JSON body keeps its status only */ }
  }
  if (response.status() < 300 && response.request().method() !== 'HEAD') {
    try {
      const buffer = Buffer.from(await response.body());
      entry.bytes = buffer.length;
      entry.magic8 = buffer.subarray(0, 8).toString('hex');
      entry.sha256 = createHash('sha256').update(buffer).digest('hex');
      // The session manifest's safe shape, when this is the session create.
      if (/\/v1\/reading\/sessions$/.test(path)) {
        try {
          const manifest = JSON.parse(buffer.toString('utf8'));
          entry.manifest = {
            format: manifest.format, size: manifest.size, revisionLength: typeof manifest.revision === 'string' ? manifest.revision.length : null,
            sequentialPresent: Object.prototype.hasOwnProperty.call(manifest, 'sequential'),
            sequential: manifest.sequential ?? null,
            hasPageNames: Array.isArray(manifest.pageNames), pageCount: Array.isArray(manifest.pageNames) ? manifest.pageNames.length : null,
          };
        } catch { entry.manifestUnparsable = true; }
      }
    } catch { entry.bodyUnreadable = true; }
  }
  resources.push(entry);
});

await page.goto(origin + '/sign-in', {waitUntil: 'domcontentloaded', timeout: 45000});
await page.locator('input[name=username]').first().waitFor({timeout: 45000});
await page.locator('input[name=username]').fill(creds.username);
await page.locator('input[type=password]').fill(creds.password);
await page.getByRole('button', {name: 'Sign in', exact: true}).click();
await page.waitForTimeout(3500);
if (/\/profiles/.test(page.url())) {
  await page.locator('button').filter({hasText: /Main profile/}).first().click({timeout: 8000});
  await page.waitForTimeout(3500);
}

const nav = page.locator('nav a[href]').filter({hasText: new RegExp(`^${library}$`, 'i')}).first();
await nav.click({timeout: 10000});
await settle(page, {quiet: 2500});
const card = page.locator('article[data-card-kind]').filter({hasText: title}).first();
const cardTitle = (await card.count()) ? await card.locator('button[aria-label]').first().getAttribute('aria-label') : null;
const read = card.getByRole('button', {name: /^Read$/}).first();
if (await read.count()) await read.click({timeout: 10000});
else { await card.locator('button[aria-label]').first().focus(); await page.keyboard.press('Enter'); }
await page.waitForTimeout(9000);

const surface = await page.evaluate(() => ({
  urlPath: location.pathname,
  textLength: (document.body.innerText || '').trim().length,
  errorPage: /Something didn't load|ran into a problem/i.test(document.body.innerText || ''),
  readerRoot: !!document.querySelector('[data-reader-root]'),
  drawnTextHead: (document.body.innerText || '').replace(/\n{2,}/g, ' | ').trim().slice(0, 220),
  backControl: !!([...document.querySelectorAll('button,a[href]')].find(e => /^(Back|Go to Home|Try again)$/i.test((e.innerText || '').trim()))),
  iframes: [...document.querySelectorAll('iframe')].map(frame => (frame.getAttribute('src') || '').split(':')[0]),
  canvases: document.querySelectorAll('canvas').length,
  images: [...document.querySelectorAll('img')].map(img => ({srcScheme: (img.currentSrc || '').split(':')[0], naturalWidth: img.naturalWidth, naturalHeight: img.naturalHeight, complete: img.complete})).slice(0, 6),
  bitmaps: window.__smokeBitmaps || [],
}));
await page.screenshot({path: `${out}/reader.png`});

const report = {title, library, cardTitle, surface, resources};
writeFileSync(`${out}/read-verify.json`, JSON.stringify(report, null, 2));
await context.close();
await browser.close();
console.log(JSON.stringify(report, null, 2));
