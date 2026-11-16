// A small Chrome DevTools Protocol driver for the TV-web checks: it starts a Chromium binary,
// opens one page and lets a check press remote keys, read what has focus, run code in the page
// and take screenshots. It uses only what every Chromium since well before 85 speaks, so the
// same checks run on the old engines LG and Samsung sets have (downloaded on GitHub's runners).
import {spawn} from 'node:child_process';
import {mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import path from 'node:path';

const KEYS = {
  up: {key: 'ArrowUp', code: 'ArrowUp', keyCode: 38}, down: {key: 'ArrowDown', code: 'ArrowDown', keyCode: 40},
  left: {key: 'ArrowLeft', code: 'ArrowLeft', keyCode: 37}, right: {key: 'ArrowRight', code: 'ArrowRight', keyCode: 39},
  ok: {key: 'Enter', code: 'Enter', keyCode: 13}, back: {key: 'Escape', code: 'Escape', keyCode: 27},
  playPause: {key: 'MediaPlayPause', code: 'MediaPlayPause', keyCode: 179},
};

export async function launch({binary, url, width = 1920, height = 1080, scratch = tmpdir(), log = () => {}, args = [], keepProfile}) {
  // A kept profile keeps the app's storage (its sign-in) from one run to the next.
  const profile = keepProfile ?? mkdtempSync(path.join(scratch, 'tvweb-chrome-'));
  const chrome = spawn(binary, ['--headless=new', '--no-sandbox', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=0', `--user-data-dir=${profile}`, `--window-size=${width},${height}`, '--autoplay-policy=no-user-gesture-required', ...args, 'about:blank'],
  {stdio: ['ignore', 'ignore', 'pipe']});
  const endpoint = await new Promise((resolve, reject) => {
    let text = '';
    const timer = setTimeout(() => reject(new Error('Chromium did not start')), 30000);
    chrome.stderr.on('data', chunk => {
      text += chunk;
      const match = /DevTools listening on (ws:\/\/\S+)/.exec(text);
      if (match) {clearTimeout(timer); resolve(match[1]);}
    });
    chrome.on('exit', code => {clearTimeout(timer); reject(new Error(`Chromium exited (${code})`));});
  });
  const base = endpoint.replace(/^ws:\/\/([^/]+)\/.*$/, 'http://$1');
  const targets = await (await fetch(`${base}/json/list`)).json();
  const page = targets.find(target => target.type === 'page');
  const socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {socket.onopen = resolve; socket.onerror = reject;});
  let id = 0;
  const pending = new Map(), handlers = new Set();
  socket.onmessage = message => {
    const data = JSON.parse(message.data);
    if (data.id && pending.has(data.id)) {
      const {resolve, reject} = pending.get(data.id); pending.delete(data.id);
      if (data.error) reject(new Error(`${data.error.message}`)); else resolve(data.result);
    } else for (const handler of handlers) handler(data);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const call = ++id; pending.set(call, {resolve, reject}); socket.send(JSON.stringify({id: call, method, params}));
  });
  const errors = [];
  handlers.add(({method, params}) => {
    if (method === 'Runtime.exceptionThrown') {
      const detail = params.exceptionDetails;
      const text = `${detail.exception?.description ?? detail.text}`;
      errors.push(text); log(`page error: ${text}`);
    } else if (method === 'Runtime.consoleAPICalled' && ['error', 'warning', 'warn'].includes(params.type)) {
      log(`console ${params.type}: ${params.args.map(arg => arg.value ?? arg.description ?? '').join(' ')}`);
    }
  });
  await send('Runtime.enable');
  await send('Page.enable');
  await send('Emulation.setDeviceMetricsOverride', {width, height, deviceScaleFactor: 1, mobile: false});
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', {expression, awaitPromise: true, returnByValue: true});
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text);
    return result.result.value;
  };
  return {
    send, evaluate, errors,
    async open(target = url) {
      await send('Page.navigate', {url: target});
    },
    async key(name, {holdMs = 0} = {}) {
      const key = KEYS[name];
      if (!key) throw new Error(`unknown key ${name}`);
      const base = {key: key.key, code: key.code, windowsVirtualKeyCode: key.keyCode, nativeVirtualKeyCode: key.keyCode};
      await send('Input.dispatchKeyEvent', {type: 'rawKeyDown', ...base});
      if (holdMs) await new Promise(resolve => setTimeout(resolve, holdMs));
      await send('Input.dispatchKeyEvent', {type: 'keyUp', ...base});
    },
    async keyDown(name, {repeat = false} = {}) {
      const key = KEYS[name];
      await send('Input.dispatchKeyEvent', {type: 'rawKeyDown', key: key.key, code: key.code, windowsVirtualKeyCode: key.keyCode, nativeVirtualKeyCode: key.keyCode, autoRepeat: repeat});
    },
    async keyUp(name) {
      const key = KEYS[name];
      await send('Input.dispatchKeyEvent', {type: 'keyUp', key: key.key, code: key.code, windowsVirtualKeyCode: key.keyCode, nativeVirtualKeyCode: key.keyCode});
    },
    /** What has focus: its test ID and spoken label. */
    focused() {
      return evaluate(`(() => {const e = document.activeElement; return e && e !== document.body ? {id: e.getAttribute('data-testid') || '', label: e.getAttribute('aria-label') || e.innerText.slice(0, 80)} : null;})()`);
    },
    async waitFor(expression, timeoutMs = 15000) {
      const until = Date.now() + timeoutMs;
      for (;;) {
        if (await evaluate(expression)) return;
        if (Date.now() > until) throw new Error(`timed out waiting for ${expression}`);
        await new Promise(resolve => setTimeout(resolve, 100));
      }
    },
    async screenshot() {
      const {data} = await send('Page.captureScreenshot', {format: 'png'});
      return Buffer.from(data, 'base64');
    },
    async close() {
      try { socket.close(); } catch { /* gone */ }
      // Close the browser through DevTools so it writes the page's storage before it goes.
      const exited = new Promise(resolve => chrome.once('exit', resolve));
      try {
        const browser = new WebSocket(endpoint);
        await new Promise((resolve, reject) => {browser.onopen = resolve; browser.onerror = reject;});
        browser.send(JSON.stringify({id: 1, method: 'Browser.close'}));
      } catch { chrome.kill('SIGTERM'); }
      const timer = setTimeout(() => chrome.kill('SIGKILL'), 10000);
      await exited;
      clearTimeout(timer);
      if (!keepProfile) rmSync(profile, {recursive: true, force: true});
    },
  };
}
