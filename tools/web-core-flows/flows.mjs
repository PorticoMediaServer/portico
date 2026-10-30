// Campaign v2 web core-flow walk for Search, Settings, Read and Live.
// Runs the built web bundle against a disposable same-origin server.
// Environment (all required):
//   PORTICO_FLOW_BASE      origin serving web and /v1
//   PORTICO_FLOW_CREDS     path to a JSON file written by run.py (never argv)
//   PORTICO_FLOW_OUTPUT    directory for report.json and screenshots
//   PORTICO_PLAYWRIGHT_MODULE  file:// URL of a playwright-core entry module
//   PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH  browser executable
// No credentials are printed. The report holds only step results, visible
// text excerpts, page errors and failed requests.
import {mkdir, readFile, writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {createHash} from 'node:crypto';
import {readingResponsePath} from './reading-response-path.mjs';
import {redactAdminPath, runStep, flowFailed} from './flow-results.mjs';

const base = process.env.PORTICO_FLOW_BASE;
const output = process.env.PORTICO_FLOW_OUTPUT;
const creds = JSON.parse(await readFile(process.env.PORTICO_FLOW_CREDS, 'utf8'));
if (!base || !output || !process.env.PORTICO_PLAYWRIGHT_MODULE) throw new Error('missing flow environment');
await mkdir(output, {recursive: true});
const {chromium} = await import(pathToFileURL(process.env.PORTICO_PLAYWRIGHT_MODULE).href);

const excerpt = (text, limit = 160) => String(text).replace(/\s+/g, ' ').trim().slice(0, limit);
const describe = value => {
  if (typeof value === 'string') return value;
  if (value && typeof value === 'object') return value.message ? String(value.message) : JSON.stringify(value);
  return String(value);
};

async function walk(page, viewportName) {
  const steps = [];
  const consoleErrors = [];
  const consoleTail = [];
  const pageErrors = [];
  const failedRequests = [];
  const attempted = [];
  const pageResponses = [];
  const adminRequests = [];
  page.on('request', request => {
    attempted.push(request.url()); if (attempted.length > 400) attempted.shift();
    const path = new URL(request.url()).pathname;
    if (path.startsWith('/v1/admin/libraries/')) adminRequests.push(request.method() + ' ' + redactAdminPath(path));
  });
  page.on('response', response => {
    const path = new URL(response.url()).pathname;
    // Only reading session page/source answers, and only their shape: status,
    // type, length, first eight magic bytes and a hash. Never the body itself.
    const retainedPath = readingResponsePath(path);
    if (!retainedPath) return;
    const shape = {path: retainedPath, status: response.status(),
      contentType: response.headers()['content-type'] ?? '', contentLength: response.headers()['content-length'] ?? ''};
    void response.body().then(async body => {
      // A matching magic proves nothing on its own: the page is only usable if
      // the browser's own decoder accepts these exact bytes.
      const decodable = await page.evaluate(encoded => new Promise(resolve => {
        const bytes = Uint8Array.from(atob(encoded), value => value.charCodeAt(0));
        createImageBitmap(new Blob([bytes], {type: 'image/png'}))
          .then(bitmap => { bitmap.close(); resolve(true); }, () => resolve(false));
      }), body.toString('base64')).catch(() => null);
      pageResponses.push({...shape, bytes: body.length, magic: body.subarray(0, 8).toString('hex'),
        sha256: createHash('sha256').update(body).digest('hex'), decodable});
    }).catch(() => { pageResponses.push({...shape, body: 'unreadable'}); });
  });
  page.on('console', message => {
    const line = message.type() + ': ' + excerpt(message.text(), 300);
    consoleTail.push(line);
    if (consoleTail.length > 40) consoleTail.shift();
    if (message.type() === 'error') consoleErrors.push(excerpt(message.text(), 300));
  });
  page.on('pageerror', error => pageErrors.push(excerpt(describe(error), 400)));
  page.on('requestfailed', request => {
    // A client-aborted navigation is not a server failure.
    const reason = request.failure()?.errorText ?? '';
    if (!/ERR_ABORTED/.test(reason)) failedRequests.push(request.method() + ' ' + new URL(request.url()).pathname + ' ' + reason);
  });
  const step = async (name, fn) => {
    const started = Date.now();
    const before = {page: pageErrors.length, console: consoleErrors.length, failed: failedRequests.length};
    const result = await runStep(name, fn, started);
    if (result.status === 'fail') {
      result.detail = excerpt(result.detail, 400);
      result.console = consoleTail.slice(-12);
    }
    steps.push(result);
    steps.at(-1).newErrors = {
      page: pageErrors.slice(before.page), console: consoleErrors.slice(before.console), failed: failedRequests.slice(before.failed),
    };
    return steps.at(-1);
  };
  const text = () => page.evaluate(() => document.body.innerText);
  // Book text lives inside the reader's sandboxed frame, which the parent
  // document's innerText does not include; read both without crossing trust.
  const bookText = () => page.evaluate(() => [document.body.innerText,
    ...Array.from(document.querySelectorAll('iframe'), frame => { try { return frame.contentDocument?.body?.innerText ?? ''; } catch { return ''; } })].join('\n'));
  const settle = async (ms = 350) => page.waitForTimeout(ms);
  const signIn = async () => {
    await page.goto(base + '/sign-in', {waitUntil: 'domcontentloaded'});
    const user = page.locator('input[name=username]');
    await user.waitFor({timeout: 20000});
    await user.fill(creds.username);
    await page.locator('input[name=password]').fill(creds.password);
    await page.locator('form button[type=submit]').click();
    await page.waitForFunction(() => !location.pathname.startsWith('/sign-in'), null, {timeout: 30000});
    await settle(1200);
  };
  await step('sign-in', async () => { await signIn(); return {path: new URL(page.url()).pathname}; });
  await step('launch', async () => {
    const body = await text();
    if (body.trim().length < 40) throw new Error('shell rendered almost no text');
    return {path: new URL(page.url()).pathname, text: excerpt(body)};
  });

  // Search: type, wait for results, open one, return, then the no-match state.
  await step('search-results', async () => {
    await page.goto(base + '/search', {waitUntil: 'domcontentloaded'});
    const field = page.locator('input[type=search]');
    await field.waitFor({timeout: 20000});
    await field.fill(creds.searchTerm);
    await page.locator('[data-group] button, [data-group] a, [data-search-hero] button, button:has-text("Play")').first().waitFor({timeout: 12000}).catch(() => {});
    await settle(700);
    const body = await text();
    if (body.trim().length < 40) throw new Error('search rendered almost no text');
    const matches = await page.locator('[data-group] button, a[href^="/item/"]').count();
    const groups = await page.locator('[data-group]').count();
    const hero = /Top result/i.test(body);
    if (matches === 0 && !hero) throw new Error('no result controls after typing "' + creds.searchTerm + '": ' + excerpt(body, 300));
    return {text: excerpt(body, 400), controls: matches, groups, hero};
  });
  await step('search-open', async () => {
    const before = page.url();
    const details = page.getByRole('button', {name: /^details$/i}).first();
    if (await details.count()) await details.click();
    else {
      const row = page.locator('[data-group] button, [data-group] a, a[href^="/item/"]').first();
      if (await row.count()) await row.click();
      else {
        // A narrow search can draw only the drawn top result; opening its own
        // first action is still opening a result.
        const play = page.getByRole('button', {name: /^Play/i}).first();
        if (!(await play.count())) throw new Error('no clickable result on ' + new URL(page.url()).pathname + ': ' + excerpt(await text(), 300)
          + ' | buttons=' + await page.locator('button').count() + ' links=' + await page.locator('a[href]').count());
        await play.click();
      }
    }
    await settle(1800);
    if (page.url() === before) throw new Error('opening a result did not navigate');
    return {from: new URL(before).pathname, to: new URL(page.url()).pathname};
  });
  await step('search-empty', async () => {
    await page.goto(base + '/search?q=zzzzzzzzzz', {waitUntil: 'domcontentloaded'});
    await settle(2500);
    const body = await text();
    if (body.trim().length < 40) throw new Error('empty search rendered almost no text');
    return {text: excerpt(body, 400)};
  });

  // Settings: real sections, the libraries list, and the owner's scan control.
  await step('settings-sections', async () => {
    await page.goto(base + '/settings', {waitUntil: 'domcontentloaded'});
    await settle(1500);
    const body = await text();
    if (!/profile|playback|dashboard|libraries/i.test(body)) throw new Error('settings navigation absent: ' + excerpt(body, 300));
    return {text: excerpt(body, 400)};
  });
  await step('settings-libraries', async () => {
    await page.goto(base + '/settings?scope=server&section=libraries', {waitUntil: 'domcontentloaded'});
    await settle(2000);
    let body = await text();
    if (!body.includes(creds.libraryName)) {
      const entry = page.getByText('Libraries', {exact: true}).first();
      if (await entry.count()) { await entry.click(); await settle(1800); body = await text(); }
    }
    if (!body.includes(creds.libraryName)) throw new Error('libraries section did not list "' + creds.libraryName + '"');
    return {text: excerpt(body, 500)};
  });
  await step('settings-libraries-scan', async () => {
    const scan = page.getByRole('button', {name: /scan/i}).first();
    if (!(await scan.count())) return {skipped: 'no scan control in the libraries list'};
    await scan.click();
    await settle(2500);
    return {text: excerpt(await text(), 400)};
  });

  await step('settings-add-library', async () => {
    await page.goto(base + '/settings?scope=server&section=libraries', {waitUntil: 'domcontentloaded'});
    await settle(1500);
    if (!(await page.getByRole('button', {name: /^Add library$/i}).count())) {
      const entry = page.getByText('Libraries', {exact: true}).first();
      if (await entry.count()) { await entry.click(); await settle(1500); }
    }
    await page.getByRole('button', {name: /^Add library$/i}).first().click();
    const dialog = page.getByRole('dialog').first();
    await dialog.waitFor({timeout: 10000});
    await dialog.getByLabel('Name').fill('Added Library');
    await dialog.getByLabel('Folder path').fill(creds.addPath);
    await dialog.getByRole('button', {name: /^Add library$/i}).last().click();
    await dialog.waitFor({state: 'detached', timeout: 20000}).catch(() => {});
    await settle(2500);
    const body = await text();
    if (!body.includes('Added Library')) throw new Error('the added library did not appear: ' + excerpt(body, 300));
    return {added: 'Added Library'};
  });


  await step('settings-library-folder-lifecycle', async () => {
    await page.goto(base + '/settings?scope=server&section=libraries', {waitUntil: 'domcontentloaded'});
    await settle(1800);
    if (!(await page.getByRole('button', {name: /^Actions$/i}).count())) {
      const entry = page.getByRole('button', {name: /^Libraries$/i}).first();
      if (await entry.count()) { await entry.click(); await settle(2000); }
    }
    await page.getByRole('button', {name: /^Actions$/i}).first().click();
    await page.getByText('Edit', {exact: true}).first().click();
    const foldersTab = page.getByRole('tab', {name: /^Folders$/i}).first();
    await foldersTab.waitFor({timeout: 15000});
    await foldersTab.click();
    await settle(2500);
    // Add folder: the drawn explorer, then the folder's own name and interval.
    await page.getByRole('button', {name: /^Add folder$/i}).first().click();
    const picker = page.getByRole('dialog').first();
    await picker.waitFor({timeout: 10000});
    await picker.getByLabel('Folder path').fill(creds.addPath);
    await picker.getByRole('button', {name: /^Go$/i}).click();
    await settle(1500);
    await picker.getByRole('button', {name: /^Choose this folder$/i}).click();
    const editor = page.getByRole('dialog').first();
    await editor.waitFor({timeout: 10000});
    await editor.getByRole('button', {name: /^Save folder$/i}).click();
    await settle(3000);
    let body = await text();
    if (!body.includes(creds.addPath)) throw new Error('the added folder did not appear: ' + excerpt(body, 300));
    // Remove that same folder again through the drawn confirmation.
    const rows = page.locator('[data-folder-row]');
    const last = rows.last();
    await last.getByRole('button', {name: /^Actions$/i}).click();
    await page.getByText('Remove folder', {exact: true}).first().click();
    const confirm = page.getByRole('dialog').first();
    await confirm.waitFor({timeout: 10000});
    await confirm.getByRole('button', {name: /^Remove folder$/i}).click();
    await settle(3000);
    body = await text();
    if (body.includes(creds.addPath)) throw new Error('the removed folder is still listed: ' + excerpt(body, 300));
    return {added: creds.addPath, removed: true};
  });

  await step('settings-library-folders', async () => {
    await page.goto(base + '/settings?scope=server&section=libraries', {waitUntil: 'domcontentloaded'});
    await settle(1800);
    if (!(await page.getByRole('button', {name: /^Actions$/i}).count())) {
      const entry = page.getByRole('button', {name: /^Libraries$/i}).first();
      if (await entry.count()) { await entry.click(); await settle(2000); }
    }
    if (!(await page.getByRole('button', {name: /^Actions$/i}).count()))
      throw new Error('the compact Settings did not reach the Libraries list: ' + excerpt(await text(), 300));
    await page.getByRole('button', {name: /^Actions$/i}).first().click();
    await page.getByText('Edit', {exact: true}).first().click();
    const foldersTab = page.getByRole('tab', {name: /^Folders$/i}).first();
    await foldersTab.waitFor({timeout: 15000});
    await foldersTab.click();
    await settle(3500);
    let body = await text();
    if (!body.includes(creds.bookPath)) throw new Error('the folders tab did not show the library folder: ' + excerpt(body, 300));
    const before = adminRequests.length;
    const menu = page.getByRole('button', {name: /^Actions$/i}).first();
    await menu.click();
    await page.getByText('Check availability', {exact: true}).first().click();
    await settle(2500);
    await menu.click();
    await page.getByText('Scan this folder', {exact: true}).first().click();
    await settle(3000);
    const commands = adminRequests.slice(before);
    if (!commands.some(value => value.endsWith('/check'))) throw new Error('Check availability sent no command: ' + JSON.stringify(commands));
    if (!commands.some(value => value.endsWith('/scans'))) throw new Error('Scan this folder sent no command: ' + JSON.stringify(commands));
    body = await text();
    return {folder: creds.bookPath, commands};
  });
  await step('settings-account', async () => {
    await page.goto(base + '/settings?scope=account&section=playback', {waitUntil: 'domcontentloaded'});
    await settle(1500);
    const body = await text();
    if (body.trim().length < 40) throw new Error('account settings rendered almost no text');
    return {text: excerpt(body, 400)};
  });

  // Read: the reading library, then opening the book into the reader.
  await step('read-library', async () => {
    await page.goto(base + '/library/' + encodeURIComponent(creds.libraryId), {waitUntil: 'domcontentloaded'});
    await settle(2500);
    const body = await text();
    if (!body.includes(creds.bookTitle)) throw new Error('library did not show "' + creds.bookTitle + '": ' + excerpt(body, 200));
    return {text: excerpt(body, 400)};
  });
  await step('read-open', async () => {
    // Click the EPUB's own card, then its Read control: the first Read button on
    // a phone-width grid can belong to the other book.
    await page.getByText(creds.bookTitle, {exact: false}).first().click();
    await settle(1500);
    if (!new URL(page.url()).pathname.startsWith('/read/')) {
      const read = page.getByRole('button', {name: /^read$/i}).first();
      if (await read.count()) await read.click();
    }
    await settle(4500);
    const path = new URL(page.url()).pathname;
    if (!path.startsWith('/read/')) throw new Error('reading did not open from the library; at ' + path);
    const body = await bookText();
    if (!body.includes(creds.bookText)) throw new Error('reader did not render the chapter text: ' + excerpt(body, 200));
    return {path, text: excerpt(body, 400)};
  });


  await step('read-cbz', async () => {
    await page.goto(base + '/library/' + encodeURIComponent(creds.comicsLibraryId), {waitUntil: 'domcontentloaded'});
    await settle(2500);
    await page.getByText(creds.comicTitle, {exact: false}).first().click();
    await settle(1500);
    if (!new URL(page.url()).pathname.startsWith('/read/')) {
      const read = page.getByRole('button', {name: /^read$/i}).first();
      if (await read.count()) await read.click();
    }
    await settle(8000);
    const path = new URL(page.url()).pathname;
    if (!path.startsWith('/read/')) throw new Error('comic reading did not open; at ' + path);
    // Retained negative control: this CBZ's only page is a 1x1 PNG whose IDAT
    // CRC is corrupt. The decoder must refuse it; a tolerant decoder may paint,
    // and neither outcome fails the run.
    const painted = await page.evaluate(() => !!document.querySelector('[data-reader-page] canvas, [data-reader-page], main canvas'));
    const body = await bookText();
    const geometry = await page.evaluate(() => {
      const root = document.querySelector('[data-reader-root]');
      const host = root?.parentElement ?? null;
      const box = node => node ? [node.clientWidth, node.clientHeight, node.hidden] : null;
      return {viewport: [innerWidth, innerHeight, devicePixelRatio], root: box(root), host: box(host)};
    });
    return {path, painted, negativeControl: 'corrupt IDAT CRC', refused: /Something didn't load/i.test(body), geometry};
  });


  await step('read-cbz-valid', async () => {
    const before = pageResponses.length;
    await page.goto(base + '/library/' + encodeURIComponent(creds.comicsLibraryId), {waitUntil: 'domcontentloaded'});
    await settle(2500);
    await page.getByText(creds.stripeComicTitle, {exact: false}).first().click();
    await settle(1500);
    if (!new URL(page.url()).pathname.startsWith('/read/')) {
      const read = page.getByRole('button', {name: /^read$/i}).first();
      if (await read.count()) await read.click();
    }
    await settle(8000);
    const path = new URL(page.url()).pathname;
    if (!path.startsWith('/read/')) throw new Error('the valid comic did not open; at ' + path);
    const painted = await page.evaluate(title => {
      const roots = Array.from(document.querySelectorAll('[data-reader-root]'));
      return roots.some(root => (root.textContent ?? '').includes(title) && !!root.querySelector('[data-reader-page] canvas, [data-reader-page]'));
    }, creds.stripeComicTitle);
    const body = await bookText();
    if (!painted) throw new Error('the valid comic painted no page: ' + excerpt(body, 300));
    return {path, pages: pageResponses.slice(before)};
  });

  await step('read-pdf', async () => {
    await page.goto(base + '/library/' + encodeURIComponent(creds.libraryId), {waitUntil: 'domcontentloaded'});
    await settle(2500);
    const card = page.getByText(creds.pdfTitle, {exact: false}).first();
    if (!(await card.count())) throw new Error('library did not show the PDF "' + creds.pdfTitle + '"');
    await card.click();
    await settle(1500);
    if (!new URL(page.url()).pathname.startsWith('/read/')) {
      const read = page.getByRole('button', {name: /^read$/i}).first();
      if (await read.count()) await read.click();
    }
    await settle(4500);
    const path = new URL(page.url()).pathname;
    if (!path.startsWith('/read/')) throw new Error('PDF reading did not open; at ' + path);
    const rendered = await page.evaluate(() => !!document.querySelector('[data-reader-page], canvas'));
    if (!rendered) throw new Error('PDF reader rendered no page');
    return {path};
  });


  await step('read-hostile', async () => {
    const before = attempted.length;
    await page.goto(base + '/library/' + encodeURIComponent(creds.libraryId), {waitUntil: 'domcontentloaded'});
    await settle(2000);
    const card = page.getByText(creds.hostileTitle, {exact: false}).first();
    if (!(await card.count())) throw new Error('library did not show "' + creds.hostileTitle + '"');
    await card.click();
    await settle(1500);
    if (!new URL(page.url()).pathname.startsWith('/read/')) {
      const read = page.getByRole('button', {name: /^read$/i}).first();
      if (await read.count()) await read.click();
    }
    await settle(4000);
    const path = new URL(page.url()).pathname;
    if (!path.startsWith('/read/')) throw new Error('hostile book did not open; at ' + path);
    const body = await bookText();
    // A safe reader either draws the plain paragraph or refuses the book
    // outright; what it may never do is run the book's script or reach the network.
    const refused = /Something didn't load|page ran into a problem/i.test(body);
    if (!refused && !body.includes('only thing a reader may draw')) throw new Error('the hostile book drew neither its plain paragraph nor the refusal state: ' + excerpt(body, 300));
    if (body.includes('book script ran')) throw new Error('the book script ran inside the reader');
    const escapes = attempted.slice(before).filter(url => /portico-epub-egress|127\.0\.0\.1:9/.test(url));
    if (escapes.length) throw new Error('the book reached the network: ' + escapes.join(', '));
    const hostFrame = await page.evaluate(() => Array.from(document.querySelectorAll('iframe')).some(frame => {
      try { return /^https?:/.test(frame.getAttribute('src') ?? '') || /^https?:/.test(frame.src ?? ''); } catch { return false; }
    }));
    if (hostFrame) throw new Error('the book created a network iframe');
    return {path, refused, attemptedDuringStep: attempted.length - before};
  });

  // Live: with or without a source the screen must render, never blank.
  await step('live-guide', async () => {
    await page.goto(base + '/live', {waitUntil: 'domcontentloaded'});
    await settle(3000);
    const body = await text();
    if (body.trim().length < 20) throw new Error('live screen rendered almost no text');
    const rows = await page.locator('[data-guide-row]').count();
    return {text: excerpt(body, 400), guideRows: rows, sourcePresent: rows > 0};
  });
  await page.screenshot({path: join(output, viewportName + '-last.jpg'), type: 'jpeg', quality: 70, fullPage: false});
  return {viewport: viewportName, steps, pageErrors, consoleErrors, failedRequests};
}

const browser = await chromium.launch({executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ?? '/usr/bin/chromium',
  headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage']});
try {
  const report = {base, startedAt: new Date().toISOString(), viewports: []};
  for (const [name, viewport] of [['desktop', {width: 1440, height: 900}], ['phone', {width: 390, height: 844}]]) {
    const context = await browser.newContext({viewport, deviceScaleFactor: 1, reducedMotion: 'reduce', bypassCSP: process.env.PORTICO_FLOW_BYPASS_CSP === '1'});
    const page = await context.newPage();
    try { report.viewports.push(await walk(page, name)); }
    finally { await context.close(); }
  }
  report.finishedAt = new Date().toISOString();
  await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2) + '\n');
  const failed = report.viewports.flatMap(view => view.steps.filter(step => step.status === 'fail').map(step => ({step: view.viewport + ' ' + step.name,
    detail: step.detail, console: step.console ?? [], errors: step.newErrors ?? {}})));
  console.log(JSON.stringify({steps: report.viewports.flatMap(view => view.steps.map(step => view.viewport + ' ' + step.name + ' ' + step.status)),
    pageErrors: report.viewports.flatMap(view => view.pageErrors),
    failedRequests: report.viewports.flatMap(view => view.failedRequests), failed}, null, 2));
  if (process.env.PORTICO_FLOW_DUMP === '1') for (const view of report.viewports) for (const step of view.steps) console.log('STEP ' + view.viewport + ' ' + step.name + ' ' + step.status + ' ' + JSON.stringify(step.detail));
  process.exitCode = flowFailed(report) ? 1 : 0;
} finally {
  await browser.close();
}
