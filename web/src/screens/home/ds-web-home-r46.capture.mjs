#!/usr/bin/env node
/**
 * DEV-only same-size/same-state capture for the Home specimen.
 *
 * It renders the pinned design frame (mock.html#home) and the genuine HomeView specimen
 * (ds-web-home-r46.specimen.tsx) in one headless Chromium, at the frame's 1440px design
 * width, with reduced motion, the same Manrope file and the same reference artwork.
 * It is a screenshot pair, not a comparator: it writes PNGs plus the measured boxes that
 * are needed to read them.
 *
 * Inputs:  PORTICO_RUNNER_OUTPUT (PNG destination), PORTICO_RUNNER_SECONDARY_SOURCE
 *          (the pinned private design repo; never copied into this repo).
 * Options: --chromium /path  (or PORTICO_CHROMIUM), --only reference|actual.
 */
import {createServer} from 'node:http';
import {spawn} from 'node:child_process';
import {readFile, writeFile, mkdir, rm} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {extname, join, resolve, sep} from 'node:path';

const output = process.env.PORTICO_RUNNER_OUTPUT;
if (!output) throw new Error('PORTICO_RUNNER_OUTPUT is required');
const designRepo = process.env.PORTICO_RUNNER_SECONDARY_SOURCE;
if (!designRepo) throw new Error('PORTICO_RUNNER_SECONDARY_SOURCE is required (pinned design repo)');
const root = resolve(import.meta.dirname, '../../../..');
const frameRoot = join(designRepo, 'design/ui-v1');
if (!existsSync(join(frameRoot, 'mock.html'))) throw new Error('Pinned frame root is missing mock.html');
const options = Object.fromEntries(process.argv.slice(2).reduce((list, value, index, all) => index % 2 === 0 && value.startsWith('--') ? [...list, [value.slice(2), all[index + 1]]] : list, []));
const chromiumPath = options.chromium ?? process.env.PORTICO_CHROMIUM ??
  ['/usr/bin/chromium', '/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/snap/bin/chromium'].find(existsSync);
if (!chromiumPath) throw new Error('No Chromium executable found; pass --chromium');
const only = options.only, slide = Number(options.slide ?? 0);
const viewportWidth = 1440;
const fontFile = join(root, 'web/src/kit/fonts/manrope-var-latin.woff2');

const types = {'.html': 'text/html; charset=utf-8', '.css': 'text/css; charset=utf-8', '.js': 'text/javascript', '.mjs': 'text/javascript', '.json': 'application/json',
  '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.png': 'image/png', '.svg': 'image/svg+xml', '.webp': 'image/webp', '.woff2': 'font/woff2', '.avif': 'image/avif'};
const frameServer = createServer(async (request, response) => {
  const path = new URL(request.url, 'http://127.0.0.1').pathname;
  const headers = {'Access-Control-Allow-Origin': '*', 'Cache-Control': 'no-store'};
  try {
    const file = path === '/font/manrope.woff2' ? fontFile : join(frameRoot, decodeURIComponent(path));
    if (file !== fontFile && !file.startsWith(frameRoot + sep)) throw Object.assign(new Error('outside'), {code: 403});
    const body = await readFile(file);
    response.writeHead(200, {...headers, 'Content-Type': types[extname(file)] ?? 'application/octet-stream', 'Content-Length': body.length});
    response.end(body);
  } catch { response.writeHead(404, headers); response.end(); }
});
const framePort = await new Promise(done => frameServer.listen(0, '127.0.0.1', () => done(frameServer.address().port)));
const frameOrigin = `http://127.0.0.1:${framePort}`;

const wait = ms => new Promise(done => setTimeout(done, ms));
async function serve(build) {
  const route = `http://127.0.0.1:${build.port}`;
  for (let attempt = 0; attempt < 120; attempt++) {
    try { if ((await fetch(route)).status < 500) return route; } catch {}
    await wait(250);
  }
  throw new Error('vite did not start: ' + viteLog.slice(-400));
}
const vitePort = 5400 + Math.floor(Math.random() * 300);
const vite = spawn(process.execPath, [join(root, 'node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', String(vitePort), '--strictPort'], {cwd: join(root, 'web'), stdio: ['ignore', 'pipe', 'pipe']});
Object.assign(vite, {port: vitePort});
let viteLog = '';
vite.stdout.on('data', bytes => viteLog += bytes);
vite.stderr.on('data', bytes => viteLog += bytes);
const buildOrigin = await serve(vite);

const profile = join(process.env.TMPDIR ?? '/tmp', `ds-web-home-r46-browser-${process.pid}`);
const browser = spawn(chromiumPath, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu', '--hide-scrollbars', '--no-first-run',
  '--no-default-browser-check', '--force-device-scale-factor=1',
  '--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4',
  '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0',
  `--user-data-dir=${profile}`, 'about:blank'], {stdio: ['ignore', 'ignore', 'pipe']});
let browserLog = '';
browser.stderr.on('data', bytes => browserLog += bytes);

let socket, sequence = 0;
const pending = new Map(), sessionEvents = new Map();
function call(method, params = {}, sessionId) {
  const id = ++sequence;
  return new Promise((done, fail) => {
    pending.set(id, {done, fail, timer: setTimeout(() => { pending.delete(id); fail(new Error(`CDP timeout: ${method}`)); }, 30_000)});
    socket.send(JSON.stringify({id, method, params, ...(sessionId ? {sessionId} : {})}));
  });
}
async function connect() {
  for (let attempt = 0; attempt < 200; attempt++) {
    const active = join(profile, 'DevToolsActivePort');
    if (existsSync(active)) {
      const [port, path] = (await readFile(active, 'utf8')).trim().split('\n');
      socket = new WebSocket(`ws://127.0.0.1:${port}${path}`);
      await new Promise((done, fail) => { socket.addEventListener('open', done, {once: true}); socket.addEventListener('error', fail, {once: true}); });
      socket.addEventListener('message', event => {
        const message = JSON.parse(event.data);
        if (message.id && pending.has(message.id)) {
          const entry = pending.get(message.id); pending.delete(message.id); clearTimeout(entry.timer);
          message.error ? entry.fail(new Error(`${message.error.message} (${entry.method ?? ''})`)) : entry.done(message.result);
        } else if (message.method) (sessionEvents.get(message.sessionId) ?? []).forEach(handler => handler(message));
      });
      return;
    }
    await wait(100);
  }
  throw new Error('Chromium DevTools endpoint did not open');
}
await connect();
const {targetId} = await call('Target.createTarget', {url: 'about:blank'});
const {sessionId} = await call('Target.attachToTarget', {targetId, flatten: true});
const listeners = [], pageErrors = [];
listeners.push(message => { if (message.method === 'Runtime.exceptionThrown') pageErrors.push(message.params.exceptionDetails.exception?.description ?? message.params.exceptionDetails.text); });
sessionEvents.set(sessionId, listeners);
const once = (method, timeout = 30_000) => new Promise((done, fail) => {
  const timer = setTimeout(() => fail(new Error(`Timed out waiting for ${method}`)), timeout);
  const handler = message => { if (message.method !== method) return; clearTimeout(timer); listeners.splice(listeners.indexOf(handler), 1); done(message.params); };
  listeners.push(handler);
});
const evaluate = async expression => {
  const result = await call('Runtime.evaluate', {expression, awaitPromise: true, returnByValue: true}, sessionId);
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text);
  return result.result.value;
};
const setViewport = (width, height) => call('Emulation.setDeviceMetricsOverride', {width, height, deviceScaleFactor: 1, mobile: false}, sessionId);
const fontCss = `@font-face{font-family:Manrope;font-style:normal;font-weight:200 800;font-display:block;src:url('${frameOrigin}/font/manrope.woff2') format('woff2')}`;
await call('Page.enable', {}, sessionId);
await call('Runtime.enable', {}, sessionId);
const media = async features => { try { await call('Emulation.setEmulatedMedia', {features}, sessionId); } catch (error) { reportMedia.push(String(error)); } };
const reportMedia = [];
await media([{name: 'prefers-reduced-motion', value: 'reduce'}, {name: 'hover', value: 'hover'}, {name: 'pointer', value: 'fine'}]);
await call('Fetch.enable', {patterns: [{urlPattern: 'https://fonts.googleapis.com/*'}, {urlPattern: 'https://fonts.gstatic.com/*'}]}, sessionId);
listeners.push(async message => {
  if (message.method !== 'Fetch.requestPaused') return;
  const {requestId, request} = message.params;
  const body = Buffer.from(fontCss).toString('base64');
  try {
    if (request.url.startsWith('https://fonts.googleapis.com/')) await call('Fetch.fulfillRequest', {requestId, responseCode: 200, responseHeaders: [
      {name: 'Content-Type', value: 'text/css; charset=utf-8'}, {name: 'Access-Control-Allow-Origin', value: '*'}], body}, sessionId);
    else await call('Fetch.failRequest', {requestId, errorReason: 'BlockedByClient'}, sessionId);
  } catch { /* the target may already be gone */ }
});

async function open(url, ready) {
  const loaded = once('Page.loadEventFired');
  await call('Page.navigate', {url}, sessionId);
  await loaded;
  for (let attempt = 0; attempt < 120; attempt++) {
    const state = await evaluate(`(()=>{const target=document.querySelector(${JSON.stringify(ready)});if(!target)return 'missing';
      return document.fonts.status==='loaded'&&[...document.images].every(image=>image.complete&&image.naturalWidth>0)?'ready':'loading'})()`);
    if (state === 'ready') return;
    if (state === 'missing' && attempt > 40) throw new Error(`Specimen never mounted: ${ready}`);
    await wait(250);
  }
  throw new Error('Specimen did not settle');
}
async function settle() {
  let previous = -1, stable = 0;
  for (let attempt = 0; attempt < 80; attempt++) {
    const state = await evaluate(`(()=>{const images=[...document.images];return [images.length, images.filter(image=>image.complete&&image.naturalWidth>0).length, performance.getEntriesByType('resource').length]})()`);
    const key = state.join(':');
    stable = key === previous ? stable + 1 : 0;
    previous = key;
    if (stable >= 5 && state[0] === state[1] && (state[0] >= 30 || attempt > 60)) return state;
    await wait(300);
  }
  return 'unsettled';
}
const rectOf = selector => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return null;const r=el.getBoundingClientRect();
  return {x:Math.floor(r.x+scrollX),y:Math.floor(r.y+scrollY),width:Math.ceil(r.width),height:Math.ceil(r.height)}})()`);
async function shot(name, selector, pageWidth, url, ready) {
  let rect = await rectOf(selector);
  if (!rect) throw new Error(`Element not found: ${selector}`);
  // Measure at the reader's window, then reload with the whole screen in view so the
  // genuine lazy artwork is requested and painted inside the captured viewport.
  await setViewport(pageWidth, rect.y + rect.height);
  await open(url, ready);
  await settle();
  rect = await rectOf(selector);
  if (!rect) throw new Error(`Element not found after reload: ${selector}`);
  if (slide > 1) {
    // A drawn hero state: choose the nth up-next tab with a real click, as a reader would.
    const point = await evaluate(`(()=>{const tab=document.querySelectorAll('[role="tab"]')[${slide - 1}];if(!tab)return null;const r=tab.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    if (!point) throw new Error(`Hero tab ${slide} is missing`);
    await call('Input.dispatchMouseEvent', {type: 'mouseMoved', x: point.x, y: point.y, button: 'none'}, sessionId);
    await call('Input.dispatchMouseEvent', {type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1}, sessionId);
    await call('Input.dispatchMouseEvent', {type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1}, sessionId);
    await wait(1200);
    await settle();
  }
  await call('Input.dispatchMouseEvent', {type: 'mouseMoved', x: 4, y: 4, button: 'none'}, sessionId);
  await wait(300);
  const {data} = await call('Page.captureScreenshot', {format: 'png', fromSurface: true, captureBeyondViewport: true,
    clip: {x: rect.x, y: rect.y, width: rect.width, height: rect.height, scale: 1}}, sessionId);
  await writeFile(join(output, `${name}${slide > 1 ? `-slide${slide}` : ''}.png`), Buffer.from(data, 'base64'));
  return rect;
}

const report = {commit: (process.env.PORTICO_RUNNER_COMMIT ?? '').toString(), reference: {}, actual: {}, pageErrors, media: reportMedia};
await mkdir(output, {recursive: true});
const extras = options.extras === '1';
if (extras) {
  if (only !== 'actual') {
    await setViewport(2048, 1400);
    await open(`${frameOrigin}/mock.html`, '#extras');
    report.reference.extras = await shot('web-home-extras-reference', '#extras .extras > div:first-child', 2048, `${frameOrigin}/mock.html`, '#extras');
    report.reference.extrasBoxes = await evaluate(`(()=>{const root=document.querySelector('#extras .extras > div:first-child');const q=s=>{const n=root.querySelector(s);return n?n.getBoundingClientRect().toJSON():null};
      return {root:root.getBoundingClientRect().toJSON(),title:q('h3'),lead:q('.lead'),panels:[...root.querySelectorAll('.fb')].map(p=>({rect:p.getBoundingClientRect().toJSON(),label:p.querySelector('.label')?.textContent,copy:p.querySelector('.copy')?.getBoundingClientRect().toJSON(),title:p.querySelector('.lockup')?.getBoundingClientRect().toJSON(),meta:p.querySelector('.meta')?.getBoundingClientRect().toJSON()})),fonts:{title:getComputedStyle(root.querySelector('h3')).fontSize,lead:getComputedStyle(root.querySelector('.lead')).fontSize,lockup:getComputedStyle(root.querySelector('.lockup')).fontSize}}})()`);
    report.reference.extrasImages = await evaluate(`[...document.querySelectorAll('#extras .extras > div:first-child .fb')].map(p=>[...p.querySelectorAll('img')].map(i=>({src:i.getAttribute('src'),fit:getComputedStyle(i).objectFit,pos:getComputedStyle(i).objectPosition,natural:[i.naturalWidth,i.naturalHeight]})))`);
  }
  if (only !== 'reference') {
    const url = `${buildOrigin}/src/screens/home/ds-web-home-r46.html?mode=extras&assets=${encodeURIComponent(frameOrigin + '/')}`;
    await setViewport(1440, 900);
    await open(url, '[data-home-extras]');
    report.actual.extras = await shot('web-home-extras-actual', '[data-home-extras]', 1440, url, '[data-home-extras]');
    report.actual.extrasBoxes = await evaluate(`(()=>{const root=document.querySelector('[data-home-extras]');const q=s=>{const n=root.querySelector(s);return n?n.getBoundingClientRect().toJSON():null};
      return {root:root.getBoundingClientRect().toJSON(),title:q('h3'),lead:q('p'),panels:[...root.querySelectorAll('[class*="fb"]')].map(p=>({rect:p.getBoundingClientRect().toJSON(),label:p.querySelector('[class*="label"]')?.textContent,copy:p.querySelector('[class*="copy"]')?.getBoundingClientRect().toJSON(),title:p.querySelector('h4')?.getBoundingClientRect().toJSON(),meta:p.querySelector('[class*="facts"]')?.getBoundingClientRect().toJSON()})),fonts:{title:getComputedStyle(root.querySelector('h3')).fontSize,lead:getComputedStyle(root.querySelector('p')).fontSize,lockup:getComputedStyle(root.querySelector('h4')).fontSize}}})()`);
    report.actual.extrasImages = await evaluate(`[...document.querySelectorAll('[data-home-extras] [class*="fb"]')].map(p=>[...p.querySelectorAll('img')].map(i=>({src:(i.currentSrc||i.src).split('/assets/')[1]??i.currentSrc,fit:getComputedStyle(i).objectFit,pos:getComputedStyle(i).objectPosition,natural:[i.naturalWidth,i.naturalHeight]})))`);
  }
} else {
if (only !== 'actual') {
  await setViewport(2048, 1400);
  await open(`${frameOrigin}/mock.html`, '#home');
  report.reference.zoom = await evaluate(`getComputedStyle(document.querySelector('#home')).zoom`);
  report.reference.rect = await shot('web-home-r46-reference', '#home', 2048, `${frameOrigin}/mock.html`, '#home');
  report.reference.pointer = await evaluate(`({hover: matchMedia('(hover: hover)').matches, fine: matchMedia('(pointer: fine)').matches})`);
  report.reference.rows = await evaluate(`[...document.querySelectorAll('#home .rows > .row')].map(row=>{const r=row.getBoundingClientRect(),track=row.querySelector('.track'),head=row.querySelector('.row-head');
    return {heading:head?.querySelector('h2')?.textContent??'', top:Math.round(r.top), height:Math.round(r.height), headBox:head?Math.round(head.getBoundingClientRect().height):null, trackPadding:track?getComputedStyle(track).paddingLeft:null,
      firstCard:track?.firstElementChild?Math.round(track.firstElementChild.getBoundingClientRect().width):null, cards:track?.children.length??0, link:head?.querySelector('.all')?.textContent??null, linkOpacity:head?.querySelector('.all')?getComputedStyle(head.querySelector('.all')).opacity:null}})`.replace(/\n/g, ' '));
  report.reference.collection = await evaluate(`(()=>{const el=document.querySelector('#home .banner');if(!el)return null;const r=el.getBoundingClientRect();
    const copy=el.querySelector('.in'),minis=el.querySelector('.minis');
    const action=el.querySelector('.in .btn');
    return {rect:r.toJSON(),copy:copy?.getBoundingClientRect().toJSON()??null,minis:minis?.getBoundingClientRect().toJSON()??null,action:action?.getBoundingClientRect().toJSON()??null}})()`);
  report.reference.poster = await evaluate(`(()=>{const card=document.querySelector('#home .rows > .row:nth-child(2) .track .card');if(!card)return null;
    const art=card.querySelector('.art'),title=card.querySelector('.t'),sub=card.querySelector('.s');
    return {card:card.getBoundingClientRect().toJSON(),art:art?.getBoundingClientRect().toJSON()??null,title:title?.getBoundingClientRect().toJSON()??null,subtitle:sub?.getBoundingClientRect().toJSON()??null}})()`);
  report.reference.ranked = await evaluate(`(()=>{const first=document.querySelector('#home .rows > .row:nth-child(6) .ranked');if(!first)return null;
    const number=first.querySelector('.n'),card=first.querySelector('.card');
    return {wrapper:first.getBoundingClientRect().toJSON(),number:number?.getBoundingClientRect().toJSON()??null,card:card?.getBoundingClientRect().toJSON()??null}})()`);
  report.reference.listen = await evaluate(`(()=>{const row=[...document.querySelectorAll('#home .rows > .row')].find(value=>value.querySelector('h2')?.textContent==='Listen next');if(!row)return null;
    const cards=row.querySelectorAll('.track .card'),card=cards[0],second=cards[1],r=n=>n?n.getBoundingClientRect().toJSON():null;
    return {row:r(row),head:r(row.querySelector('.row-head')),track:r(row.querySelector('.track')),card:r(card),art:r(card?.querySelector('.art')),progress:r(card?.querySelector('.prog')),title:r(card?.querySelector('.t')),subtitle:r(card?.querySelector('.s')),secondArt:r(second?.querySelector('.art'))}})()`);
  report.reference.hero = await evaluate(`(()=>{const h=document.querySelector('#home .hero'),c=h.querySelector('.copy'),th=h.querySelector('.upnext');const r=n=>n?n.getBoundingClientRect().toJSON():null;
    return {hero:r(h),copy:r(c),thumbs:r(th),firstArt:r(h.querySelector('.slide.on .bd'))}})()`);
}
if (only !== 'reference') {
  await setViewport(viewportWidth, 900);
  await open(`${buildOrigin}/src/screens/home/ds-web-home-r46.html?assets=${encodeURIComponent(frameOrigin + '/')}`, '[data-home-document]');
  const actualUrl = `${buildOrigin}/src/screens/home/ds-web-home-r46.html?assets=${encodeURIComponent(frameOrigin + '/')}`;
  report.actual.rect = await shot('web-home-r46-actual', '[data-home-document]', viewportWidth, actualUrl, '[data-home-document]');
  report.actual.pointer = await evaluate(`({hover: matchMedia('(hover: hover)').matches, fine: matchMedia('(pointer: fine)').matches})`);
  report.actual.artwork = await evaluate(`(()=>{const all=[...document.querySelectorAll('[data-artwork-status]')],counts={};for(const node of all){const key=node.dataset.artworkStatus;counts[key]=(counts[key]??0)+1;}
    const fetches=performance.getEntriesByType('resource').filter(entry=>entry.name.includes('/assets/')).map(entry=>entry.name.split('/assets/')[1]);
    return {total:all.length,counts,images:document.images.length,fetches:fetches.length,first:fetches.slice(0,3),sample:all.slice(0,3).map(node=>({status:node.dataset.artworkStatus,hasImage:!!node.querySelector('img')}))}})()`);
  report.actual.collection = await evaluate(`(()=>{const el=document.querySelector('[data-home-document] [data-card-kind="collectionBanner"]');if(!el)return null;const r=el.getBoundingClientRect();
    const copy=el.querySelector('[class*="collectionCopy"]'),minis=el.querySelector('[class*="collectionMinis"]');
    const action=el.querySelector('[class*="collectionAction"] button');
    return {rect:r.toJSON(),copy:copy?.getBoundingClientRect().toJSON()??null,minis:minis?.getBoundingClientRect().toJSON()??null,action:action?.getBoundingClientRect().toJSON()??null}})()`);
  report.actual.poster = await evaluate(`(()=>{const card=[...document.querySelectorAll('[data-home-document] [data-card-kind="poster"]')].find(el=>el.closest('section'));
    if(!card)return null;const art=card.querySelector('[class*="art"]'),title=card.querySelector('[class*="title"]'),cap=card.querySelector('[class*="caption"]'),sub=card.querySelector('[class*="subtitle"]');
    return {card:card.getBoundingClientRect().toJSON(),art:art?.getBoundingClientRect().toJSON()??null,title:title?.getBoundingClientRect().toJSON()??null,caption:cap?.getBoundingClientRect().toJSON()??null,subtitle:sub?.getBoundingClientRect().toJSON()??null}})()`);
  report.actual.ranked = await evaluate(`(()=>{const first=[...document.querySelectorAll('[data-home-document] [class*="scaledRanked"]')][0];if(!first)return null;
    const number=first.children[0]?.children[0],card=first.querySelector('[data-card-kind="poster"]');
    return {wrapper:first.getBoundingClientRect().toJSON(),number:number?.getBoundingClientRect().toJSON()??null,card:card?.getBoundingClientRect().toJSON()??null}})()`);
  report.actual.listen = await evaluate(`(()=>{const section=[...document.querySelectorAll('[data-home-document] section[data-platform]')].find(value=>value.querySelector('h2')?.textContent==='Listen next');if(!section)return null;
    const cards=section.querySelectorAll('article[data-card-kind="square"]'),card=cards[0],second=cards[1],r=n=>n?n.getBoundingClientRect().toJSON():null;
    return {row:r(section),head:r(section.querySelector('header')),track:r(section.querySelector('div[class*="track"]')),card:r(card),art:r(card?.querySelector('button > span[class*="art"]')),progress:r(card?.querySelector('button > [class*="progress"]')),title:r(card?.querySelector('[class*="title"]')),subtitle:r(card?.querySelector('[class*="subtitle"]')),secondArt:r(second?.querySelector('button > span[class*="art"]'))}})()`);
  report.actual.rows = await evaluate(`[...document.querySelectorAll('[data-home-document] section[data-platform]')].map(row=>{const r=row.getBoundingClientRect(),track=row.querySelector('div[class*="track"]'),head=row.querySelector('header');
    return {heading:head?.querySelector('h2')?.textContent??'', top:Math.round(r.top), height:Math.round(r.height), headBox:head?Math.round(head.getBoundingClientRect().height):null, trackPadding:track?getComputedStyle(track).paddingLeft:null,
      firstCard:track?.firstElementChild?Math.round(track.firstElementChild.getBoundingClientRect().width):null, cards:track?.children.length??0, link:head?.querySelector('a,button')?.textContent??null, linkOpacity:head?.querySelector('a,button')?getComputedStyle(head.querySelector('a,button')).opacity:null}})`.replace(/\n/g, ' '));
  report.actual.hero = await evaluate(`(()=>{const h=document.querySelector('[data-home-document] section'),c=h.querySelector('div[class*="copy"]'),th=h.querySelector('div[class*="thumbnail"]');const r=n=>n?n.getBoundingClientRect().toJSON():null;
    const wash=h.querySelector('[class*="washLayer"]'),ambient=h.querySelector('[class*="ambientImage"]');
    return {hero:r(h),copy:r(c),thumbs:r(th),firstArt:r(h.querySelector('[data-active="true"] [data-foreground-art]')),washMask:wash?getComputedStyle(wash).maskImage:null,ambient:ambient?{status:ambient.closest('[data-artwork-status]')?.dataset.artworkStatus??null,hasImage:!!ambient.querySelector('img')}:null}})()`);
}
}
report.vite = viteLog.slice(-400);
await writeFile(join(output, 'web-home-r46-capture.json'), JSON.stringify(report, null, 2) + '\n');
console.log('CAPTURE ' + JSON.stringify(report));

try { await call('Target.closeTarget', {targetId}); } catch {}
socket.close();
browser.kill('SIGTERM');
vite.kill('SIGTERM');
await wait(500);
await rm(profile, {recursive: true, force: true});
frameServer.close();
if (browserLog.includes('FATAL')) console.log('BROWSER ' + browserLog.slice(-400));
