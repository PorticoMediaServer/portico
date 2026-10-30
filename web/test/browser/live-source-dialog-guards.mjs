// Authored UNRUN rendered-flow vectors for the live source dialogs. They mount the real
// OwnerSourcesPage in StrictMode under Vite and drive source replacement, retained callbacks,
// the five-second refresh timer and a new usable opening; they are not executed here and claim
// no runtime result.
import assert from 'node:assert/strict';
import {mkdir, mkdtemp, writeFile, rm, rmdir} from 'node:fs/promises';
import {createServer} from 'vite';
import {chromium} from 'playwright';
await mkdir('.scratch', {recursive: true});
const scratch = await mkdtemp('.scratch/live-source-guards-'), entry = scratch + '/entry.tsx';
let server, browser;
try {
  const source = `import React from 'react';
import {createRoot} from 'react-dom/client';
import {OwnerSourcesPage} from '/src/screens/live/owner/OwnerSourcesPage';
const io = {loads: {}, disposes: {}, requests: []}, timers = [];
const realSetTimeout = window.setTimeout.bind(window);
window.setTimeout = (fn, ms, ...rest) => {if (ms === 5000) {timers.push(fn);return -1;}return realSetTimeout(fn, ms, ...rest);};
// One stable defaults snapshot: a fresh object per getSnapshot would loop useSyncExternalStore.
const defaultsSnapshot = Object.freeze({saving: false, dirty: false, draft: Object.freeze({
  streamBufferMs: 1000, retryWindowMs: 5000, guideDays: 7, userAgent: '', logoImport: false, discoveryEnabled: false,
  importLimits: Object.freeze({channels: 8, programmes: 1000, bytes: 1048576})})});
const make = name => {
  const listeners = new Set();let current = true;
  const snapshot = {loading: false, busy: false, error: '', receipt: null,
    document: {sources: [], refresh: [{state: 'refreshing'}]}};
  const store = {subscribe: fn => {listeners.add(fn);return () => listeners.delete(fn);}, getSnapshot: () => snapshot};
  const defaults = {subscribe: () => () => {}, getSnapshot: () => defaultsSnapshot, load: async () => {},
    save: async () => {}, set: () => {}, discard: () => {}};
  return {
    api: {request: async (...args) => {io.requests.push({name, args});return new Promise(() => {});}},
    serverId: 'server', current: () => current,
    sources: {...store, load: async () => {io.loads[name] = (io.loads[name] ?? 0) + 1;}, act: async () => false, next: async () => {}},
    defaults,
    subscribe: fn => {listeners.add(fn);return () => listeners.delete(fn);},
    track: value => value, release: () => {},
    dispose: () => {io.disposes[name] = (io.disposes[name] ?? 0) + 1;},
    reportError: () => {},
  };
};
const first = make('A'), second = make('B');let active = first;
const root = createRoot(document.getElementById('root'));
const render = () => root.render(<React.StrictMode><OwnerSourcesPage source={active}/></React.StrictMode>);
window.probe = {io, timers, replace: () => {active = second;render();}, unmount: () => root.render(null)};
render();`;
  await writeFile(entry, source);
  server = await createServer({server: {port: 0, strictPort: false}});
  server.middlewares.use('/__live_source_guards', async (_req, res) => {
    res.setHeader('Content-Type', 'text/html');
    res.end(await server.transformIndexHtml('/__live_source_guards', '<html><body><div id="root"></div><script type="module" src="/' + entry + '"></script></body></html>'));
  });
  await server.listen();
  browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || '/snap/bin/chromium', args: ['--no-sandbox'], chromiumSandbox: false});
  const page = await browser.newPage({viewport: {width: 1440, height: 900}});
  await page.goto('http://127.0.0.1:' + server.httpServer.address().port + '/__live_source_guards');
  const retain = async (locator, key) => locator.evaluate((node, key) => {
    const props = Object.keys(node).find(value => value.startsWith('__reactProps$'));
    window[key] = node[props].onClick ?? node[props].onSelect;
  }, key);
  const add = page.getByRole('button', {name: 'Add source', exact: true}).first();
  await add.waitFor();
  // Retain the actual A add/menu callback (not a deferred locator).
  await add.click();
  const menuItem = page.getByRole('menuitem', {name: /^Playlist URL or tuner/});
  await retain(menuItem, 'oldAdd');
  await menuItem.click();
  const dialog = page.getByRole('dialog');
  await dialog.waitFor();
  // A's Check must be otherwise admissible: an empty draft would make the
  // zero-I/O assertion pass through validation even without retirement.
  await dialog.getByLabel('Name', {exact: true}).fill('Original A');
  await dialog.getByLabel('Playlist URL', {exact: true}).fill('https://fixture.invalid/a.m3u');
  assert.equal(await dialog.getByRole('button', {name: 'Check source', exact: true}).isEnabled(), true);
  await retain(dialog.getByRole('button', {name: 'Cancel', exact: true}), 'oldCancel');
  await retain(dialog.getByRole('button', {name: 'Check source', exact: true}), 'oldCheck');
  // Capture A's real five-second refresh callback before replacement.
  await page.evaluate(() => {window.oldTimers = window.probe.timers.slice();});
  assert.ok(await page.evaluate(() => window.oldTimers.length >= 1));
  // Replace the source while A.current() deliberately stays true.
  await page.evaluate(() => window.probe.replace());
  await page.waitForFunction(() => !document.querySelector('[role="dialog"]'));
  assert.deepEqual(await page.evaluate(() => window.probe.io.disposes), {});
  assert.deepEqual(await page.evaluate(() => window.probe.io.loads), {A: 1, B: 1});
  // Retained old A callbacks must not close/mutate anything or make I/O.
  await page.evaluate(() => {window.oldCancel({});window.oldCheck({});window.oldAdd({});});
  await page.waitForTimeout(0);
  assert.equal(await page.evaluate(() => window.probe.io.requests.length), 0);
  // The replaced parent still opens a usable B dialog, and A's retained callbacks cannot clear it.
  const addB = page.getByRole('button', {name: 'Add source', exact: true}).first();
  await addB.click();
  await page.getByRole('menuitem', {name: /^Playlist URL or tuner/}).click();
  const dialogB = page.getByRole('dialog');
  await dialogB.waitFor();
  await dialogB.getByLabel('Name', {exact: true}).fill('Successor B');
  await dialogB.getByLabel('Playlist URL', {exact: true}).fill('https://fixture.invalid/b.m3u');
  assert.equal(await dialogB.getByRole('button', {name: 'Check source', exact: true}).isEnabled(), true);
  await page.evaluate(() => {window.oldCancel({});window.oldCheck({});window.oldAdd({});});
  await page.waitForTimeout(0);
  assert.equal(await dialogB.count(), 1);
  assert.equal(await dialogB.getByLabel('Name', {exact: true}).inputValue(), 'Successor B');
  assert.equal(await dialogB.getByLabel('Playlist URL', {exact: true}).inputValue(), 'https://fixture.invalid/b.m3u');
  assert.equal(await page.evaluate(() => window.probe.io.requests.length), 0);
  // A captured old refresh timer may not load the retired A source.
  assert.equal(await page.evaluate(() => document.visibilityState), 'visible');
  const before = await page.evaluate(() => window.probe.io.loads);
  await page.evaluate(() => {for (const fn of window.oldTimers) fn();});
  await page.waitForTimeout(0);
  assert.deepEqual(await page.evaluate(() => window.probe.io.loads), before);
} finally {
  await browser?.close().catch(() => {});
  await server?.close().catch(() => {});
  await rm(entry, {force: true}).catch(() => {});
  await rmdir(scratch).catch(() => {});
}
