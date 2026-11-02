import test from 'node:test';
import assert from 'node:assert/strict';
import {forgetDestination, servedServerOrigin} from '../src/platform/served-origin.ts';

const page = (href: string) => {const url = new URL(href); return {protocol: url.protocol, hostname: url.hostname, origin: url.origin};};

test('a page a Portico server served names that server; web.getportico.tv and the desktop app do not', () => {
  assert.equal(servedServerOrigin(page('https://demo.getportico.tv/sign-in'), {hostedWeb: false}), 'https://demo.getportico.tv');
  assert.equal(servedServerOrigin(page('http://192.168.1.20:32500/servers'), {hostedWeb: false}), 'http://192.168.1.20:32500');
  assert.equal(servedServerOrigin(page('https://web.getportico.tv/'), {hostedWeb: true}), undefined);
  assert.equal(servedServerOrigin(page('tauri://localhost/'), {hostedWeb: false}), undefined);
  assert.equal(servedServerOrigin(page('http://tauri.localhost/'), {hostedWeb: false}), undefined);
  // The development proxy names the server it stands in for.
  assert.equal(servedServerOrigin(page('http://127.0.0.1:5174/'), {hostedWeb: false, serverUrl: 'http://127.0.0.1:32500'}), 'http://127.0.0.1:32500');
});

test('after Forget, a served page signs in to its own server; only an unserved page opens the chooser', () => {
  assert.deepEqual(forgetDestination('https://demo.getportico.tv'), {to: 'sign-in', origin: 'https://demo.getportico.tv'});
  assert.deepEqual(forgetDestination(undefined), {to: 'servers'});
});
