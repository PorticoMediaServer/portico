/** Apple's App Transport Security: plain HTTP on the local network only, said plainly otherwise. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {checkApplePlainHttp, localPlainHttpHost} from '../src-next/platform/apple-plain-http.ts';
import {RouteError} from '../../../packages/client-core/src/route-identity.ts';

test('local addresses may use plain HTTP', () => {
  for (const host of ['192.168.1.20', '10.0.0.5', '172.20.1.1', '127.0.0.1', '169.254.3.4', '100.101.102.103', '[::1]', '[fe80::1]', '[fd12:3456::1]', 'nas', 'localhost', 'portico.local', 'media.home.arpa', 'box.internal', 'server.lan']) {
    assert.equal(localPlainHttpHost(host), true, host);
  }
});

test('remote addresses must use HTTPS', () => {
  for (const host of ['203.0.113.9', '8.8.8.8', '172.32.0.1', '100.128.0.1', '[2001:db8::1]', 'media.example.com', 'portico.ts.net', '999.1.1.1']) {
    assert.equal(localPlainHttpHost(host), false, host);
  }
  assert.throws(() => checkApplePlainHttp('http://media.example.com:32500/v1/system'), (e: unknown) => e instanceof RouteError && e.code === 'plain_http_remote');
  assert.doesNotThrow(() => checkApplePlainHttp('https://media.example.com:32500/v1/system'));
  assert.doesNotThrow(() => checkApplePlainHttp('http://192.168.1.20:32500/v1/system'));
});

test('the Apple apps allow local networking, not arbitrary loads', () => {
  for (const path of ['../ios/Portico/Info.plist', '../tvos/Portico/Info.plist', '../tvos/TopShelf/Info.plist']) {
    const plist = readFileSync(new URL(path, import.meta.url), 'utf8');
    assert.doesNotMatch(plist, /NSAllowsArbitraryLoads/, path);
    assert.match(plist, /<key>NSAllowsLocalNetworking<\/key>\s*<true\/>/, path);
  }
});
