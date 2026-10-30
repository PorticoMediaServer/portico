import {writeFileSync, appendFileSync, mkdirSync} from 'node:fs';
import path from 'node:path';

/** Dependency-free CDP client. The browser and application must be disposable local instances. */
export class ChromeDriver {
  static async connect(port = 32762, output) {
    const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    const target = targets.find(t => t.type === 'page');
    if (!target) throw new Error('Chrome has no page target');
    const driver = new ChromeDriver(target.webSocketDebuggerUrl, output);
    await driver.ready;
    await driver.send('Runtime.enable');
    await driver.send('Log.enable');
    await driver.send('Page.enable');
    await driver.send('Network.enable');
    await driver.send('Fetch.enable', {patterns: [{urlPattern: '*'}]});
    await driver.send('Page.addScriptToEvaluateOnNewDocument', {source: `document.addEventListener('securitypolicyviolation', e => console.error('CSP', e.violatedDirective, e.blockedURI));`});
    return driver;
  }
  constructor(url, output) {
    this.output = output;
    this.started = Date.now();
    this.sequence = 0;
    this.pending = new Map();
    this.events = [];
    this.requests = new Map();
    this.responses = new Map();
    this.ws = new WebSocket(url);
    this.ready = new Promise((resolve, reject) => { this.ws.onopen = resolve; this.ws.onerror = reject; });
    this.ws.onmessage = ({data}) => {
      const event = JSON.parse(data);
      if (event.id) {
        const pending = this.pending.get(event.id);
        if (!pending) return;
        clearTimeout(pending.timer);
        this.pending.delete(event.id);
        event.error ? pending.reject(new Error(JSON.stringify(event.error))) : pending.resolve(event.result);
        return;
      }
      if (event.method === 'Fetch.requestPaused') {
        const {requestId, request} = event.params;
        const url = new URL(request.url);
        const local = ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname) || ['data:', 'blob:', 'about:'].includes(url.protocol);
        if (!local) this.record({kind: 'external-blocked', url: request.url});
        this.send(local ? 'Fetch.continueRequest' : 'Fetch.failRequest', local ? {requestId} : {requestId, errorReason: 'BlockedByClient'}).catch(() => {});
      }
      if (event.method === 'Network.requestWillBeSent') this.requests.set(event.params.requestId, event.params.request.url);
      if (event.method === 'Network.loadingFinished') {
        const url = this.requests.get(event.params.requestId);
        if (url && /subtitles|playback-offers/.test(url) && !url.includes('/media/')) this.send('Network.getResponseBody', {requestId:event.params.requestId}).then(body=>{try{this.responses.set(url,JSON.parse(body.body));}catch{}}).catch(()=>{});
      }
      if (event.method === 'Runtime.exceptionThrown' && event.params.timestamp >= this.started) this.record({kind: 'exception', detail: event.params.exceptionDetails});
      if (event.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(event.params.type) && event.params.timestamp >= this.started) this.record({kind: 'console', detail: event.params});
      if (event.method === 'Log.entryAdded' && ['error', 'warning'].includes(event.params.entry.level) && event.params.entry.timestamp >= this.started) this.record({kind: 'log', detail: event.params.entry});
      if (event.method === 'Network.loadingFailed') this.record({kind: 'network', url: this.requests.get(event.params.requestId), detail: event.params});
      if (event.method === 'Network.responseReceived' && event.params.response.status >= 400) this.record({kind: 'http', url: event.params.response.url, status: event.params.response.status});
    };
  }
  record(event) {
    this.events.push(event);
    if (this.output) { mkdirSync(this.output, {recursive: true}); appendFileSync(path.join(this.output, 'browser-events.jsonl'), JSON.stringify({at: new Date().toISOString(), ...event}) + '\n'); }
  }
  send(method, params = {}) {
    const id = ++this.sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP timed out: ${method}`)); }, 20000);
      this.pending.set(id, {resolve, reject, timer});
      this.ws.send(JSON.stringify({id, method, params}));
    });
  }
  async evaluate(expression) {
    const result = await this.send('Runtime.evaluate', {expression, returnByValue: true, awaitPromise: true});
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  }
  async navigate(url) { await this.send('Page.navigate', {url}); }
  async waitFor(expression, timeout = 15000) {
    const until = Date.now() + timeout;
    while (Date.now() < until) {
      try { if (await this.evaluate(`!!(${expression})`)) return; } catch {}
      await new Promise(resolve => setTimeout(resolve, 150));
    }
    throw new Error(`Timed out waiting for ${expression}`);
  }
  async viewport(width, height) { await this.send('Emulation.setDeviceMetricsOverride', {width, height, screenWidth: width, screenHeight: height, deviceScaleFactor: 1, mobile: width < 600}); }
  async screenshot(name) {
    const {data} = await this.send('Page.captureScreenshot', {format: 'png', captureBeyondViewport: false});
    const file = path.join(this.output, 'screenshots', name + '.png');
    mkdirSync(path.dirname(file), {recursive: true}); writeFileSync(file, Buffer.from(data, 'base64')); return file;
  }
  async clickText(text, selector = 'button,a,[role="menuitem"],[role="tab"],[role="radio"]', index = 0) {
    const point = await this.evaluate(`(() => {const el = [...document.querySelectorAll(${JSON.stringify(selector)})].filter(e => e.getClientRects().length && (e.innerText.trim() === ${JSON.stringify(text)} || e.getAttribute('aria-label') === ${JSON.stringify(text)}))[${index}]; if (!el) throw new Error('Control not found: ' + ${JSON.stringify(text)}); el.scrollIntoView({block:'center'}); const r=el.getBoundingClientRect(); return {x:r.x+r.width/2,y:r.y+r.height/2};})()`);
    await this.send('Input.dispatchMouseEvent', {type: 'mouseMoved', ...point});
    await new Promise(resolve => setTimeout(resolve, 120));
    await this.send('Input.dispatchMouseEvent', {type: 'mousePressed', ...point, button: 'left', clickCount: 1});
    await this.send('Input.dispatchMouseEvent', {type: 'mouseReleased', ...point, button: 'left', clickCount: 1});
  }
  async clickSelector(selector, index = 0) {
    const point = await this.evaluate(`(() => {const el = [...document.querySelectorAll(${JSON.stringify(selector)})].filter(e=>e.getClientRects().length)[${index}]; if (!el) throw new Error('Control not found'); el.scrollIntoView({block:'center'});const r=el.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2};})()`);
    await this.send('Input.dispatchMouseEvent', {type:'mouseMoved', ...point});
    await new Promise(resolve=>setTimeout(resolve,120));
    await this.send('Input.dispatchMouseEvent', {type:'mousePressed', ...point,button:'left',clickCount:1});
    await this.send('Input.dispatchMouseEvent', {type:'mouseReleased', ...point,button:'left',clickCount:1});
  }
  async fill(selector, value) {
    return this.evaluate(`(() => {const el = document.querySelector(${JSON.stringify(selector)}); if (!el) throw new Error('Input not found'); const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, ${JSON.stringify(value)}); el.dispatchEvent(new Event('input', {bubbles:true})); el.dispatchEvent(new Event('change', {bubbles:true})); return true;})()`);
  }
  async inspect() {
    return this.evaluate(`({url:location.href, title:document.title, text:document.body.innerText, mainText:document.querySelector('main')?.innerText, controls:[...document.querySelectorAll('input,select,button,a,[role="tab"]')].filter(e=>e.getClientRects().length).map(e=>({tag:e.tagName,type:e.type,id:e.id,text:e.innerText,aria:e.getAttribute('aria-label'),placeholder:e.placeholder,href:e.getAttribute('href'),value:e.type==='password'?'[redacted]':e.value,disabled:e.disabled}))})`);
  }
  close() { this.ws.close(); }
}
if (import.meta.url === `file://${process.argv[1]}`) {
  const driver = await ChromeDriver.connect(Number(process.env.PORTICO_CHROME_PORT || 32762), process.env.PORTICO_BROWSER_OUTPUT);
  try {
    const input = await new Promise(resolve => { let text = ''; process.stdin.on('data', d => text += d); process.stdin.on('end', () => resolve(text)); });
    const run = new Function('driver', `return (async () => {${input}\n})();`);
    const result = await run(driver);
    if (result !== undefined) console.log(JSON.stringify(result, null, 2));
  } finally { driver.close(); }
}
