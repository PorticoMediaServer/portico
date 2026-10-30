import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {exposeCredentials} from '../src/bridge/credential-boundary.ts';
(globalThis as {require?: unknown}).require = createRequire(new URL('../src/bridge/runtime.ts', import.meta.url));
const {PorticoRuntime} = await import('../src/bridge/runtime.ts');
class Request {
  static made: Request[] = [];
  readyState = 0; status = 200; responseText = '{}'; responseURL = ''; responseType = ''; withCredentials = false; timeout = 0;
  url = ''; method = ''; headers: Record<string, string> = {};
  onload?: () => void; onabort?: () => void; onreadystatechange?: () => void;
  constructor() {Request.made.push(this);}
  open(method: string, url: string) {this.method = method; this.url = url;}
  setRequestHeader(name: string, value: string) {this.headers[name] = value;}
  getResponseHeader(_name: string) {return null;}
  getAllResponseHeaders() {return '';}
  send() {}
  abort() {this.onabort?.();}
  respond() {this.responseURL = this.url; this.readyState = 2; this.onreadystatechange?.(); this.onload?.();}
}
(globalThis as {XMLHttpRequest?: unknown}).XMLHttpRequest = Request;

test('profile/PIN requests accept non-loopback plain HTTP and retain route, size and method bounds', async () => {
  const request = PorticoRuntime.profileRequest('pin', 'http://remote.example:32500/v1/profiles/select', 'POST', {}, '{"pin":"1234"}');
  Request.made.at(-1)!.respond(); assert.equal((await request).status, 200);
  for (const [url, method, body] of [
    ['ftp://remote.example/v1/profiles/select', 'POST', '{}'],
    ['http://u:p@remote.example/v1/profiles/select', 'POST', '{}'],
    ['http://remote.example/outside', 'POST', '{}'],
    ['http://remote.example/v1/profiles/select', 'TRACE', '{}'],
    ['http://remote.example/v1/profiles/select', 'POST', 'x'.repeat(65537)],
  ]) await assert.rejects(PorticoRuntime.profileRequest('invalid', url, method, {}, body));
});

test('remembered viewer credentials are origin bound; exposure quarantines them and dispatches revocation', async () => {
  const record = {version: 1, serverUrl: 'http://remembered.example:32500', session: {accessToken: 'remembered-access', refreshToken: 'remembered-refresh', sessionFamilyId: 'remembered-family'}};
  await PorticoRuntime.saveSelectedViewer(JSON.stringify(record));
  assert.equal(await PorticoRuntime.loadSelectedViewer(), JSON.stringify(record));
  const before = Request.made.length;
  await assert.rejects(PorticoRuntime.networkRequest('wrong', 'https://different.example/v1/me', 'GET', {Authorization: 'Bearer remembered-access'}, null));
  assert.equal(Request.made.length, before);
  exposeCredentials(record.serverUrl + '/v1/me');
  assert.equal(Request.made.at(-1)!.method, 'DELETE');
  assert.equal(Request.made.at(-1)!.url, record.serverUrl + '/v1/sessions/current');
  Request.made.at(-1)!.respond();
  assert.equal(await PorticoRuntime.loadSelectedViewer(), null);
  await assert.rejects(PorticoRuntime.saveSelectedViewer(JSON.stringify(record)), 'late persistence cannot restore exposed credentials');
  assert.equal(await PorticoRuntime.loadSelectedViewer(), null);
});

test('Vega limitation and exposure messages come from its small catalogue', () => {
  assert.equal(PorticoRuntime.transportNotice('en-CA'), 'This device can check redirects only after a request has been sent.');
  assert.equal(PorticoRuntime.credentialExposureMessage('en-US'), 'This connection sent your sign-in details to another address, so you have been signed out.');
});
