// The TV-web checks in real browsers, for CI (.github/workflows/tv-web.yml) and by hand:
//
//   TVWEB_BROWSERS='chromium85=/path/chrome,chromium87=/path/chrome,chrome=/usr/bin/google-chrome' \
//   PORTICO_SERVER_NEXT_BIN=… PORTICO_SEED_BIN=… node test/browser/run.mjs [focus] [timing] [playback]
//
// For each browser: a fresh test server (server.mjs) behind the built app (front.mjs, dist/ must be
// built), sign-in with the remote, then
//   focus     the written focus tests for Home and the rail (focus-map.mjs);
//   timing    holding Right along a 12-card stretch of a Home row, a key repeat every 150 ms, with
//             the CPU slowed 1x, 4x and 8x: the time from each key to the frame after focus moved;
//   playback  the test film plays (decoded frames, time advancing) in a browser with H.264.
// A JSON report goes to TVWEB_REPORT (default report.json) and a summary to stdout and, on GitHub,
// to the job summary. Timing is reported, not judged; the focus tests and playback are.
import {appendFileSync, mkdirSync, writeFileSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {launch} from '../cdp.mjs';
import {startFront} from './front.mjs';
import {startServer} from './server.mjs';
import {measureRowsScript, names, openOwnerProfile, remote, signIn} from './session.mjs';
import {focusMapTests, ROWS} from './focus-map.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const app = path.resolve(here, '../..');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const scratch = process.env.RUNNER_TEMP ?? process.env.TVWEB_SCRATCH ?? path.join(app, '.check');
mkdirSync(scratch, {recursive: true});
const parts = new Set(process.argv.slice(2).length ? process.argv.slice(2) : ['focus', 'timing', 'playback']);
const browsers = (process.env.TVWEB_BROWSERS ?? 'chrome=/usr/bin/google-chrome').split(',').map(entry => entry.split('='));
const report = {browsers: {}};

class NotRun extends Error {}
/** A test the app itself cannot reach on this server (an app finding, the same on every TV). */
class Blocked extends Error {}

async function session(binary, legacyHeadless) {
  const server = await startServer({serverBin: process.env.PORTICO_SERVER_NEXT_BIN, seedBin: process.env.PORTICO_SEED_BIN, scratch});
  // The focus map's long row ("New on your server") is second: the profile's saved row order.
  await server.call('PATCH', '/v1/preferences', {scope: 'profile-server', deviceClass: 'television', values: {'home.rowOrder': [ROWS.CW, ROWS.NEW, ROWS.REC]}}, server.token);
  const front = await startFront({dist: path.join(app, 'dist'), server: server.origin});
  const page = remote(await launch({binary, url: front.origin, scratch, headless: legacyHeadless ? '--headless' : '--headless=new', log: () => {}}));
  // A closed app is a page that called window.close(): remembered, so a check can ask.
  await page.send('Page.addScriptToEvaluateOnNewDocument', {source: 'window.close = () => { window.__tvwebClosed = true; };'});
  await page.open();
  await signIn(page, {address: front.origin, username: server.username, password: server.password});
  const close = async () => {await page.close(); await front.stop(); await server.stop();};
  return {server, front, page, close};
}

function context({server, front, page}) {
  let rows = {}, pending = [], failures = [], release;
  const n = names(row => rows[row] ?? []);
  const reread = async () => {
    const home = await server.call('GET', '/v1/home', undefined, server.token);
    rows = Object.fromEntries(home.rows.map(row => [row.id, row.items.map(item => item.id)]));
  };
  const now = () => page.now();
  const c = {
    page, n, now, reread,
    rotationMs: 9500,
    films: undefined,
    order: row => rows[row] ?? [],
    loaded: row => (rows[row] ?? []).length,
    keys: async (...keys) => {for (const key of keys) await page.press(key);},
    hold: async () => {await page.press('ok', {holdMs: 800}); await sleep(600);},
    expect(locator) {pending.push(now().then(focus => {if (!locator.test(focus)) failures.push(`expected ${locator.what}, focus is on ${focus ? `${focus.label || '(no label)'} [${focus.region}.${focus.child}]` : 'nothing'}`);}));},
    expectMenu() {pending.push(now().then(focus => {if (!/menu|sheet|overlay/i.test(`${focus?.region} ${focus?.id}`)) failures.push(`focus is not in a menu: ${focus?.label} [${focus?.region}]`);}));},
    check(ok, message) {if (!ok) failures.push(message);},
    notRun(reason) {throw new NotRun(reason);},
    /** A test that goes on in the card menu a previous test opened. */
    async inCardMenu() {if (!/menu/.test((await now())?.region ?? '') && !/menu/.test((await now())?.id ?? '')) throw new Blocked('the card menu it goes on in did not open (see F-HOME-40)');},
    /** The card menu on this server offers only Select (no Resume, Mark as watched or See all). */
    async cardMenu() {
      await c.hold();
      const items = await page.evaluate(`(() => { const menu = document.activeElement?.closest('[data-tv-trap-down]'); return menu ? Array.from(menu.querySelectorAll('[tabindex="0"]')).map(e => (e.innerText || '').trim()) : []; })()`);
      if (!items.some(label => /^Resume|watched/i.test(label))) {await page.press('back'); throw new Blocked(`app finding: the card menu offers only ${items.join(', ') || 'nothing'} (the focus map draws Resume, Mark as watched, …, See all)`);}
    },
    async waitFocus(locator, ms = 5000) {
      for (const until = Date.now() + ms; Date.now() < until; await sleep(150)) if (locator.test(await now())) return;
    },
    closed: async () => {await sleep(400); return page.evaluate('window.__tvwebClosed === true');},
    async home() {
      await page.evaluate('location.reload()');
      await sleep(1500);
      // A relaunch asks who is watching (this owner has two profiles).
      await openOwnerProfile(page);
      await sleep(1500);
      await page.evaluate(measureRowsScript);
      await reread();
      c.films ??= `library:${(await server.call('GET', '/v1/libraries', undefined, server.token)).items.find(library => library.name === 'Films').id}`;
    },
    async top() {
      for (let n2 = 0; n2 < 5 && !n.spotlight('play').test(await now()); n2++) await page.press('back');
    },
    async onHome() {return page.evaluate(`(() => {const e = document.querySelector('[data-testid*="home.spotlight"]'); return !!e && e.getBoundingClientRect().width > 0;})()`);},
    async inSlot() {const focus = await now(); return !!focus && Math.abs(focus.left - 200) <= 30;},
    async playerOpen() {await page.waitFor(`!!document.querySelector('video') || /watch|player/i.test(document.activeElement?.getAttribute('data-testid') || '')`, 15000).catch(() => {}); return page.evaluate(`!!document.querySelector('video') || /watch|player/i.test(document.activeElement?.getAttribute('data-testid') || '')`);},
    holdRight: async repeats => {
      const before = await now();
      await page.keyDown('right');
      for (let n2 = 1; n2 < repeats; n2++) {await sleep(150); await page.keyDown('right', {repeat: true});}
      await sleep(150); await page.keyUp('right'); await sleep(1200);
      const after = await now();
      return {steps: (after?.index ?? NaN) - (before?.index ?? NaN)};
    },
    holdDown: async repeats => {
      const order = await page.evaluate(`Array.from(document.querySelectorAll('[tabindex="0"]')).map(e => e.getAttribute('data-testid') || '').filter(id => id.includes('%2Erail.'))`);
      const before = (await now()).id;
      await page.keyDown('down');
      for (let n2 = 1; n2 < repeats; n2++) {await sleep(150); await page.keyDown('down', {repeat: true});}
      await sleep(150); await page.keyUp('down'); await sleep(800);
      return {steps: order.indexOf((await now()).id) - order.indexOf(before)};
    },
    /** Holds the row's next page back, then moves right along it to its last loaded card (the one
     * before the skeleton), and remembers that card's place. */
    async toLastLoaded(row, {fail = false} = {}) {
      release = front.hold(new RegExp(`GET /v1/home/rows/${row}\\b`), {fail});
      for (let n2 = 0; n2 < 4 && !n.anyCard(row).test(await now()); n2++) await page.press('down');
      let last = await now();
      for (let n2 = 0; n2 < 120; n2++) {
        await page.press('right');
        const focus = await now();
        if (!n.anyCard(row).test(focus) || n.placeholder(row).test(focus) || focus.id === last.id) break;
        last = focus;
      }
      if (!n.card(row, last.index).test(await now())) await page.press('left');
      c.lastLoaded = last.index;
    },
    lastLoaded: NaN,
    async releasePages() {release?.(); release = undefined;},
    async rerank(card) {
      const id = rows[ROWS.CW][card - 1];
      await server.call('PUT', `/v1/items/${encodeURIComponent(id)}/state`, {positionMs: 9 * 60_000}, server.token);
    },
    async focusIsEntry(id) {const focus = await now(); try { return JSON.parse(focus.child)[2] === id; } catch { return false; }},
    async chooseMenu(pattern) {
      for (let n2 = 0; n2 < 14 && !pattern.test((await now())?.label ?? ''); n2++) await page.press('down');
      await page.press('ok');
    },
    async leaveOne() {
      await reread();
      for (const id of rows[ROWS.CW].slice(1)) await server.call('PUT', `/v1/items/${encodeURIComponent(id)}/state`, {watched: true}, server.token);
    },
    async openRail(item) {
      await page.press('left');
      for (let n2 = 0; n2 < 12 && (await now())?.child !== item; n2++) await page.press(item === 'search' ? 'up' : 'down');
      await page.press('ok'); await sleep(2500);
    },
    async openProfileMenu() {
      await page.press('left');
      for (let n2 = 0; n2 < 14 && !n.rail('profile').test(await now()); n2++) await page.press('down');
      await page.press('ok'); await sleep(800);
    },
    async settle() {const all = pending; pending = []; await Promise.all(all); const found = failures; failures = []; return found;},
  };
  return c;
}

async function focusChecks(state) {
  const c = context(state), results = [];
  const only = process.env.TVWEB_ONLY ? new RegExp(process.env.TVWEB_ONLY) : null;
  for (const test of focusMapTests().filter(entry => !only || only.test(entry.id))) {
    try {
      await test.run(c);
      const failed = await c.settle();
      results.push({id: test.id, result: failed.length ? 'fail' : 'pass', ...(failed.length ? {why: failed.join('; ')} : {})});
    } catch (error) {
      await c.settle().catch(() => []);
      results.push(error instanceof NotRun ? {id: test.id, result: 'not run', why: error.message} : error instanceof Blocked ? {id: test.id, result: 'blocked', why: error.message} : {id: test.id, result: 'fail', why: String(error?.message ?? error)});
    }
    const last = results.at(-1);
    console.log(`${last.result.padEnd(7)} ${last.id}${last.why ? '  ' + last.why : ''}`);
  }
  return results;
}

async function timing(state) {
  const c = context(state), {page} = state;
  const out = {};
  for (const rate of [1, 4, 8]) {
    await c.home();
    await c.keys('down', 'down');                       // the long row
    await page.evaluate(`(() => { window.__moves = []; window.addEventListener('keydown', event => {
      if (event.keyCode !== 39) return; const start = event.timeStamp;
      requestAnimationFrame(() => requestAnimationFrame(at => window.__moves.push(at - start))); }, true); return 1; })()`);
    await page.send('Emulation.setCPUThrottlingRate', {rate});
    await page.keyDown('right');
    for (let n = 1; n < 12; n++) {await sleep(150); await page.keyDown('right', {repeat: true});}
    await page.keyUp('right');
    await sleep(3000 * rate);
    await page.send('Emulation.setCPUThrottlingRate', {rate: 1});
    const moves = (await page.evaluate('window.__moves')).sort((a, b) => a - b);
    const at = q => Math.round(moves[Math.min(moves.length - 1, Math.floor(q * moves.length))]);
    out[`${rate}x`] = {keys: moves.length, medianMs: at(0.5), p90Ms: at(0.9), maxMs: Math.round(moves.at(-1))};
    console.log(`timing ${rate}x: median ${out[`${rate}x`].medianMs} ms, 90th percentile ${out[`${rate}x`].p90Ms} ms, max ${out[`${rate}x`].maxMs} ms (${moves.length} keys)`);
  }
  return out;
}

async function playback(state) {
  const c = context(state), {page} = state;
  await c.home();
  await c.keys('left', 'up');
  for (let n = 0; n < 12 && !/Test pattern/.test((await c.now())?.label ?? ''); n++) await c.keys('down');
  await c.keys('ok'); await sleep(3000);
  await c.keys('ok'); await sleep(3000);               // the grid's one film, then its page's Play
  await c.keys('ok');
  let evidence;
  for (let n = 0; n < 60; n++) {
    await sleep(500);
    evidence = await page.evaluate(`(() => {const v = document.querySelector('video'); return v ? {readyState: v.readyState, time: v.currentTime, width: v.videoWidth, paused: v.paused, error: v.error && v.error.code} : null;})()`);
    if (evidence && evidence.readyState >= 2 && evidence.width > 0 && evidence.time > 2 && !evidence.paused) break;
  }
  const plays = !!evidence && evidence.readyState >= 2 && evidence.width > 0 && evidence.time > 2 && !evidence.paused;
  console.log(`playback: ${plays ? 'plays' : 'FAILED'} ${JSON.stringify(evidence)}`);
  return {plays, evidence};
}

let failed = false;
for (const [name, binary] of browsers) {
  const legacy = /chromium8[0-9]/.test(name);
  const state = await session(binary, legacy);
  const version = await state.page.evaluate('navigator.userAgent.match(/Chrome\\/([\\d.]+)/)?.[1] ?? ""');
  const entry = report.browsers[name] = {version};
  try {
    if (parts.has('focus')) {entry.focus = await focusChecks(state); if (entry.focus.some(result => result.result === 'fail')) failed = true;}
    if (parts.has('timing')) entry.timing = await timing(state);
    if (parts.has('playback') && !legacy) {entry.playback = await playback(state); if (!entry.playback.plays) failed = true;}
  } catch (error) {
    entry.error = String(error?.stack ?? error); failed = true; console.log(entry.error);
  } finally {
    await state.close();
  }
}
writeFileSync(process.env.TVWEB_REPORT ?? path.join(scratch, 'report.json'), JSON.stringify(report, null, 2));
const lines = ['## TV web: browser checks', ''];
for (const [name, entry] of Object.entries(report.browsers)) {
  lines.push(`### ${name} (Chrome ${entry.version})`);
  if (entry.focus) {
    const count = kind => entry.focus.filter(result => result.result === kind).length;
    lines.push(`Focus map, Home and rail: ${count('pass')} pass, ${count('fail')} fail, ${count('blocked')} blocked by an app finding, ${count('not run')} not run.`);
    for (const result of entry.focus.filter(r => r.result !== 'pass')) lines.push(`- ${result.result} ${result.id}: ${result.why}`);
  }
  if (entry.timing) {
    lines.push('', '| CPU | median per move | 90th percentile | max |', '|---|---|---|---|');
    for (const [rate, t] of Object.entries(entry.timing)) lines.push(`| ${rate} | ${t.medianMs} ms | ${t.p90Ms} ms | ${t.maxMs} ms |`);
  }
  if (entry.playback) lines.push('', `Playback: ${entry.playback.plays ? 'the test film plays' : 'FAILED'} ${JSON.stringify(entry.playback.evidence)}`);
  if (entry.error) lines.push('', '```', entry.error.slice(0, 2000), '```');
  lines.push('');
}
console.log(lines.join('\n'));
if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, lines.join('\n') + '\n');
process.exit(failed ? 1 : 0);
