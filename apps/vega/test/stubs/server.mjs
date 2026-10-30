/**
 * A server for the tests, behind a stand-in for the device's XMLHttpRequest: `routes` maps an
 * address to what is answered for it (text, a segment description, a status, or a function of
 * the request). `log` is every address asked for, in order.
 *
 * An answer takes `delay` milliseconds to arrive. A test that needs to time the answer itself
 * sets `hold` to the addresses it will time: those stays open until `releaseResponse` gives its
 * status and headers and `finishResponse` gives its body, so nothing is left to the machine.
 */
import {mp4Init} from './mp4.mjs';
/** @type {{routes: Map<string, any>, log: string[], offline: boolean, delay: number, hold: false | ((url: string) => boolean), held: Request[]}} */
export const server = {routes: new Map(), log: [], offline: false, delay: 0, hold: false, held: []};
export function resetServer() { server.routes = new Map(); server.log = []; server.offline = false; server.delay = 0; server.hold = false; server.held = []; }
/** @param {{init?: boolean, pts?: number, duration?: number, bytes?: number}} description */
export const segment = description => ({segment: description});

function heldIndex(url) {
  if (server.held.length === 0) throw new Error('no response is being held');
  const index = url === undefined ? 0 : server.held.findIndex(request => request.url === url);
  if (index < 0) throw new Error(`the response for ${url} is not being held`);
  return index;
}

/** Gives a held response its status and headers; its body follows when it is finished. */
export function releaseResponse(url) { server.held[heldIndex(url)].release(); }

/** Answers a held response: any headers not yet given, its body, then its load callback. */
export function finishResponse(url) { server.held.splice(heldIndex(url), 1)[0].finish(); }

class Request {
  readyState = 0; status = 0; response = null; responseText = ''; responseURL = ''; responseType = 'text'; timeout = 0; withCredentials = false;
  onload = null; onerror = null; ontimeout = null; onabort = null; onreadystatechange = null; onprogress = null;
  #headers = {}; #answered = {}; #done = false; #released = false; #timer; /** @type {any} */ #answer;
  open(method, url) { this.method = method; this.url = url; }
  setRequestHeader(name, value) { this.#headers[name.toLowerCase()] = value; }
  getResponseHeader(name) { return this.#answered[name.toLowerCase()] ?? null; }
  getAllResponseHeaders() { return Object.entries(this.#answered).map(([k, v]) => `${k}: ${v}`).join('\r\n'); }
  #route() {
    let answer = server.routes.get(this.url);
    if (typeof answer === 'function') answer = answer({url: this.url, headers: this.#headers});
    if (answer === undefined) answer = {status: 404};
    if (typeof answer === 'string') answer = {status: 200, text: answer};
    if (answer.segment) answer = {status: 200, bytes: answer.segment.init ? mp4Init() : Buffer.from(JSON.stringify({...answer.segment, pad: 'x'.repeat(answer.segment.bytes ?? 0)}))};
    return answer;
  }
  release() {
    if (this.#done || this.#released || !this.#answer) return;
    this.#released = true;
    this.status = this.#answer.status ?? 200;
    this.responseURL = this.#answer.from ?? this.url;
    this.#answered = Object.fromEntries(Object.entries(this.#answer.headers ?? {}).map(([k, v]) => [k.toLowerCase(), v]));
    this.readyState = 2; this.onreadystatechange?.();
  }
  finish() {
    if (this.#done) return;
    clearTimeout(this.#timer);
    // An offline server gives no answer at all.
    if (!this.#answer) { this.#done = true; this.onerror?.(); return; }
    this.release();
    if (this.#done) return; // its owner gave up while reading the status
    this.#done = true;
    if (this.responseType === 'arraybuffer') { const b = this.#answer.bytes ?? Buffer.from(this.#answer.text ?? ''); this.response = b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength); }
    else this.responseText = this.#answer.text ?? '';
    this.readyState = 4; this.onreadystatechange?.();
    this.onload?.();
  }
  send() {
    server.log.push(this.url);
    this.#answer = server.offline ? null : this.#route();
    if (server.hold && server.hold(this.url)) { server.held.push(this); return; }
    this.#timer = setTimeout(() => this.finish(), server.delay);
  }
  abort() {
    if (this.#done) return;
    this.#done = true;
    clearTimeout(this.#timer);
    const index = server.held.indexOf(this);
    if (index >= 0) server.held.splice(index, 1);
    this.onabort?.();
  }
}
globalThis.XMLHttpRequest = Request;
