import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createServer} from 'node:http';
import {readFile, mkdir, writeFile} from 'node:fs/promises';
import {resolve, sep, extname} from 'node:path';
import {once} from 'node:events';

// Dependency-free runner for the provisioned Netcup Chromium. Run only inside
// the approved remote heavy wrapper, after build-display.mjs. No auth or API.
assert.ok(process.env.TMPDIR, 'owned TMPDIR required');
assert.ok(process.env.PORTICO_RUNNER_OUTPUT, 'remote collector output required');
const output = resolve(process.env.PORTICO_RUNNER_OUTPUT);
const phone = process.env.PORTICO_READER_PRESENTATION === 'phone';
const panelSelector = phone ? '[role="dialog"][aria-label="Display"]' : '[data-reader-panel]';
const root = resolve(process.env.TMPDIR, 'reader-display-build');
const profile = resolve(process.env.TMPDIR, 'reader-display-chromium');
await mkdir(profile, {recursive: true});
const server = createServer(async (request, response) => {
  const path = resolve(root, `.${decodeURIComponent(new URL(request.url, 'http://localhost').pathname)}`);
  if (!path.startsWith(root + sep)) { response.writeHead(404).end(); return; }
  try {
    const bytes = await readFile(path);
    response.writeHead(200, {'Content-Type': ({'.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.woff2': 'font/woff2', '.ttf': 'font/ttf'})[extname(path)] ?? 'application/octet-stream', 'Cache-Control': 'no-store'}).end(bytes);
  } catch { response.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1'); await once(server, 'listening');
const origin = `http://127.0.0.1:${server.address().port}`;
const chrome = spawn(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ?? '/usr/bin/chromium', [
  '--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--no-first-run', '--no-default-browser-check',
  '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank',
], {stdio: ['ignore', 'ignore', 'pipe'], detached: true});
let socket, sequence = 0, stderr = '', session;
const pending = new Map(), errors = [], observations = [];
let result;
function call(method, params = {}, sessionId = session) {
  const id = ++sequence;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 15000);
    pending.set(id, {resolve, reject, timer});
    socket.send(JSON.stringify({id, method, params, ...(sessionId ? {sessionId} : {})}));
  });
}
async function evaluate(expression) {
  const result = await call('Runtime.evaluate', {expression, awaitPromise: true, returnByValue: true});
  if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
  return result.result.value;
}
const paint = () => evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
async function click(selector) {
  const box = await evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)throw new Error('Missing control');return e.getBoundingClientRect().toJSON();})()`);
  assert.ok(box.width > 0 && box.height > 0);
  const point = {x: box.x + box.width / 2, y: box.y + box.height / 2};
  await call('Input.dispatchMouseEvent', {type: 'mouseMoved', ...point});
  await call('Input.dispatchMouseEvent', {type: 'mousePressed', button: 'left', clickCount: 1, ...point});
  await call('Input.dispatchMouseEvent', {type: 'mouseReleased', button: 'left', clickCount: 1, ...point});
  await paint();
}
async function key(key, code) {
  await call('Input.dispatchKeyEvent', {type: 'keyDown', key, code: key, windowsVirtualKeyCode: code, nativeVirtualKeyCode: code});
  await call('Input.dispatchKeyEvent', {type: 'keyUp', key, code: key, windowsVirtualKeyCode: code, nativeVirtualKeyCode: code});
  await paint();
}
const trigger = 'button[aria-label="Display: theme, font, size and layout"]';
try {
  const endpoint = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`Chromium startup timeout: ${stderr}`)), 10000);
    chrome.once('error', error => { clearTimeout(timer); reject(error); });
    chrome.stderr.on('data', chunk => {
      stderr = (stderr + chunk.toString()).slice(-8192);
      const match = stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
    chrome.once('exit', code => { clearTimeout(timer); reject(new Error(`Chromium exited ${code}: ${stderr}`)); });
  });
  socket = new WebSocket(endpoint); await once(socket, 'open');
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails);
    const task = pending.get(message.id);
    if (!task) return;
    pending.delete(message.id); clearTimeout(task.timer);
    if (message.error) task.reject(new Error(JSON.stringify(message.error))); else task.resolve(message.result);
  });
  const target = await call('Target.createTarget', {url: 'about:blank'}, null);
  session = (await call('Target.attachToTarget', {targetId: target.targetId, flatten: true}, null)).sessionId;
  await call('Page.enable'); await call('Runtime.enable');
  await call('Emulation.setDeviceMetricsOverride', {width: phone ? 390 : 820, height: phone ? 844 : 1180, deviceScaleFactor: 1, mobile: false});
  await call('Page.navigate', {url: `${origin}/test/read/display.html${phone ? '?presentation=phone' : ''}`});
  await evaluate(`new Promise((resolve,reject)=>{const until=performance.now()+10000;const check=()=>{if(document.querySelector(${JSON.stringify(panelSelector)})&&window.readerDisplayProof)document.fonts.ready.then(resolve);else if(performance.now()>until)reject(new Error('Display did not mount'));else requestAnimationFrame(check)};check()})`);
  await paint();
  if (phone) {
    await evaluate(`Promise.all(document.querySelector(${JSON.stringify(panelSelector)}).getAnimations().map(animation => animation.finished))`);
    const bounds = await evaluate(`document.querySelector(${JSON.stringify(panelSelector)}).getBoundingClientRect().toJSON()`);
    assert.equal(bounds.width, 390);
    await evaluate('document.activeElement.blur()'); await paint();
    const screenshot = await call('Page.captureScreenshot', {format: 'jpeg', quality: 94,
      clip: {x: 0, y: 258, width: 390, height: 586, scale: 1}});
    await writeFile(resolve(output, 'display-controls.jpg'), Buffer.from(screenshot.data, 'base64'));
    await click('[role=radio][value=sepia]'); await key('ArrowLeft', 37);
    assert.equal(await evaluate('document.querySelector("[role=radio][value=black]").getAttribute("aria-checked")'), 'true');
    await click('button[aria-label=Larger]');
    await click('[role=radiogroup][aria-label="Line spacing"] [value=loose]');
    const callbacks = await evaluate('window.readerDisplayProof.changes');
    for (const [key, value] of [['theme', 'black'], ['size', 6], ['spacing', 'loose']]) assert.ok(callbacks.some(change => change.key === key && change.value === value), key);
    observations.push('phone-native-arrow-and-controlled-size-spacing-callbacks');
    await key('Escape', 27);
    assert.equal(await evaluate(`!!document.querySelector(${JSON.stringify(panelSelector)})`), false);
    assert.equal(await evaluate('document.activeElement?.getAttribute("aria-label")'), 'Display: theme, font, size and layout');
    await click(trigger);
    await call('Input.dispatchMouseEvent', {type: 'mousePressed', x: 20, y: 150, button: 'left', clickCount: 1});
    await call('Input.dispatchMouseEvent', {type: 'mouseReleased', x: 20, y: 150, button: 'left', clickCount: 1}); await paint();
    assert.equal(await evaluate(`!!document.querySelector(${JSON.stringify(panelSelector)})`), false);
    assert.equal(await evaluate('window.readerDisplayProof.turns.length'), 0);
    observations.push('phone-Escape-focus-return-and-outside-dismiss-no-turn');
    await evaluate('window.disposeReaderDisplay()');
    const final = await evaluate('window.readerDisplayProof');
    assert.deepEqual(final.cleanup, {listeners: 0, DOM: 0, fontFaces: 0}); assert.deepEqual(errors, []);
    result = {status: 'PASS', observations, callbacks, final, capture: {name: 'display-controls.jpg', width: 390, height: 586, bounds},
      scope: 'controlled phone Display sheet only; no canonical Reading15, public Read session, whole frame or hardware acceptance'};
  } else {
  assert.equal(await evaluate('document.activeElement?.textContent'), 'AaSepia');
  await evaluate('Promise.all(document.querySelector("[data-reader-panel]").getAnimations().map(animation => animation.finished))');
  const bounds = await evaluate('document.querySelector("[data-reader-panel]").getBoundingClientRect().toJSON()');
  const controlMetrics = await evaluate(`(()=>{const p=document.querySelector('[data-reader-panel]'),base=p.getBoundingClientRect();return[...p.firstElementChild.children,...p.querySelectorAll('h3')].map(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return{text:e.textContent,y:r.y-base.y,height:r.height,font:s.font}})})()`);
  assert.equal(bounds.x, 460); assert.equal(bounds.y, 96); assert.equal(bounds.width, 360); assert.ok(bounds.height >= 720);
  // Match the existing reference crop's size/state. This is only the supplied
  // tablet Display controls, not a full book or accepted Read screen.
  await evaluate('document.activeElement.blur()'); await paint();
  const screenshot = await call('Page.captureScreenshot', {format: 'jpeg', quality: 94,
    clip: {x: bounds.x, y: bounds.y, width: 360, height: 720, scale: 1}});
  await writeFile(resolve(output, 'display-controls.jpg'), Buffer.from(screenshot.data, 'base64'));
  await click('[role=radio][value=sepia]');
  await key('ArrowLeft', 37);
  assert.equal(await evaluate('document.querySelector("[role=radio][value=black]").getAttribute("aria-checked")'), 'true');
  await key('Escape', 27);
  assert.equal(await evaluate('!!document.querySelector("[data-reader-panel]")'), false);
  assert.equal(await evaluate('document.activeElement?.getAttribute("aria-label")'), 'Display: theme, font, size and layout');
  observations.push('native-arrow-commit-and-escape-trigger-return');
  await click(trigger); await click('[role=switch]'); await click('button[aria-label=Larger]');
  await click('[role=radiogroup][aria-label="Line spacing"] [value=loose]');
  await click('[data-reader-panel] p button');
  const callbacks = await evaluate('window.readerDisplayProof.changes');
  for (const [key, value] of [['twoPages', false], ['size', 6], ['spacing', 'loose'], ['settings', true]]) assert.ok(callbacks.some(change => change.key === key && change.value === value), key);
  observations.push('actual-controlled-switch-size-spacing-settings-callbacks');
  await call('Input.dispatchMouseEvent', {type: 'mousePressed', x: 30, y: 300, button: 'left', clickCount: 1});
  await call('Input.dispatchMouseEvent', {type: 'mouseReleased', x: 30, y: 300, button: 'left', clickCount: 1}); await paint();
  assert.equal(await evaluate('!!document.querySelector("[data-reader-panel]")'), false);
  assert.equal(await evaluate('window.readerDisplayProof.turns.length'), 0);
  await click(trigger);
  await evaluate('window.readerDisplayInput({kind:"tap",fraction:.9})'); await paint();
  assert.equal(await evaluate('!!document.querySelector("[data-reader-panel]")'), false);
  assert.equal(await evaluate('window.readerDisplayProof.turns.length'), 0);
  await click(trigger);
  assert.equal(await evaluate('(()=>{let prevented=false;window.readerDisplayInput({kind:"key",key:"Escape",shift:false,alt:false,ctrl:false,meta:false,editable:false,preventDefault(){prevented=true}});return prevented})()'), true);
  await paint(); assert.equal(await evaluate('!!document.querySelector("[data-reader-panel]")'), false);
  observations.push('DOM-and-forwarded-page-dismiss-no-turn-and-forwarded-escape');
  await call('Emulation.setDeviceMetricsOverride', {width: 820, height: 600, deviceScaleFactor: 1, mobile: false});
  await click(trigger);
  await evaluate('Promise.all(document.querySelector("[data-reader-panel]").getAnimations().map(animation => animation.finished))');
  const scroll = await evaluate('(()=>{const p=document.querySelector("[data-reader-panel]");p.scrollTop=p.scrollHeight;return{width:p.getBoundingClientRect().width,client:p.clientHeight,scroll:p.scrollHeight,top:p.scrollTop}})()');
  assert.equal(scroll.width, 360); assert.ok(scroll.scroll > scroll.client && scroll.top > 0);
  await click('[data-reader-panel] p button'); observations.push('short-window-controls-scroll-and-settings-reachable');
  await key('Escape', 27);
  await evaluate('document.documentElement.dataset.reduceMotion="true"');
  await evaluate(`document.querySelector(${JSON.stringify(trigger)}).click()`);
  const motion = await evaluate('new Promise(resolve=>requestAnimationFrame(()=>{const p=document.querySelector("[data-reader-panel]");resolve(p.getAnimations().map(a=>({duration:a.effect.getTiming().duration,transforms:a.effect.getKeyframes().map(k=>k.transform)})))}))');
  assert.ok(motion.length > 0);
  for (const item of motion) { assert.equal(item.duration, 160); assert.ok(item.transforms.every(value => value === 'none')); }
  observations.push('supported-reduced-motion-entry');
  await evaluate('window.disposeReaderDisplay()');
  const final = await evaluate('window.readerDisplayProof');
  assert.deepEqual(final.cleanup, {listeners: 0, DOM: 0, fontFaces: 0}); assert.deepEqual(errors, []);
  result = {status: 'PASS', observations, callbacks, scroll, motion, final, capture: {name: 'display-controls.jpg', width: 360, height: 720, bounds, controlMetrics},
    scope: 'controlled components only; no canonical Reading15, public Read session, whole frame or hardware acceptance'};
  }
  let reference;
  if (process.env.PORTICO_READER_FRAME_ORIGIN) {
    const frame = new URL(phone ? '/phone-read.html?frame=6' : '/tablet-read.html?frame=6b', process.env.PORTICO_READER_FRAME_ORIGIN);
    const selector = phone ? '.rsheet[aria-label="Display"]' : '.aapanel.sidep';
    assert.equal(frame.hostname, '127.0.0.1');
    await call('Network.enable');
    await call('Network.setBlockedURLs', {urls: ['*.jpg', '*.jpeg', '*.png', '*.webp', '*.avif']});
    await call('Emulation.setDeviceMetricsOverride', {width: phone ? 1600 : 1440, height: 1330, deviceScaleFactor: 1, mobile: false});
    await call('Page.navigate', {url: frame.href});
    await evaluate(`new Promise((resolve,reject)=>{const until=performance.now()+10000;const check=()=>{if(document.querySelector(${JSON.stringify(selector)}))resolve();else if(performance.now()>until)reject(new Error('Frame did not mount'));else requestAnimationFrame(check)};check()})`);
    await evaluate(`Promise.all([document.fonts.load('600 22px Literata', 'Aa'),document.fonts.load('700 13px Manrope', 'Page')]).then(()=>document.fonts.ready)`);
    await evaluate(`document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'start',inline:'nearest'})`);
    await paint();
    reference = await evaluate(`(()=>{const p=document.querySelector(${JSON.stringify(selector)}),base=p.getBoundingClientRect();return{bounds:base.toJSON(),scroll:{x:scrollX,y:scrollY},fonts:[...document.fonts].map(f=>({family:f.family,weight:f.weight,status:f.status})),sampleFont:getComputedStyle(p.querySelector(${JSON.stringify(phone ? '.thm span' : '.th span')})).font,metrics:[...p.children,...p.querySelectorAll('h3')].map(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return{text:e.textContent,y:r.y-base.y,height:r.height,font:s.font}})}})()`);
    for (const family of ['Literata', 'Manrope']) assert.ok(reference.fonts.some(font => font.family.replaceAll('"', '') === family && font.status === 'loaded'), family);
    assert.equal(reference.bounds.width, phone ? 390 : 360);
    assert.ok(reference.bounds.y >= 0 && reference.bounds.y + (phone ? 586 : 720) <= 1330, 'reference capture must be visible');
    const frameImage = await call('Page.captureScreenshot', {format: 'jpeg', quality: 94, captureBeyondViewport: true,
      clip: {x: reference.bounds.x + reference.scroll.x, y: reference.bounds.y + reference.scroll.y, width: phone ? 390 : 360, height: phone ? 586 : 720, scale: 1}});
    await writeFile(resolve(output, 'reference-controls.jpg'), Buffer.from(frameImage.data, 'base64'));
  }
  result = {...result, reference};
  console.log('READER_DISPLAY_BROWSER ' + JSON.stringify(result));
} catch (error) {
  result = {...result, status: 'FAIL', observations, errors, failure: String(error.stack ?? error)};
  throw error;
} finally {
  for (const task of pending.values()) { clearTimeout(task.timer); task.reject(new Error('Browser closed')); }
  socket?.close();
  if (chrome.pid && chrome.exitCode === null && chrome.signalCode === null) {
    const closed = once(chrome, 'close');
    try { process.kill(-chrome.pid, 'SIGTERM'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
    await closed;
  }
  await new Promise(resolve => server.close(resolve));
  await writeFile(resolve(output, 'display-results.log'), JSON.stringify({...result, ownedBrowserClosed: true, ownedServerClosed: true}, null, 2) + '\n');
}
