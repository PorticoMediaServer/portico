import assert from 'node:assert/strict';
import {test} from 'node:test';
import {captureServicesLink, forgetServicesLink, restoreServicesLink, scrubUnsupportedServicesFragment, type ServicesLink, type ServicesLinkStore} from '../src/services/intake.ts';

function vault(initial?: ServicesLink) {
  let value = initial;
  const store: ServicesLinkStore = {read: async () => value, change: async update => { value = update(value); }};
  return {store, value: () => value};
}

test('device and mail fragments are removed before any asynchronous continuation work', () => {
  for (const [pathname, hash, value] of [['/device', '#code=7kqm4txp', '7KQM-4TXP'], ['/reset', '#token=secret_123', 'secret_123'], ['/verify-email', '#token=secret_123', 'secret_123']]) {
    let scrubbed = false;
    const result = captureServicesLink({pathname, hash, search: ''}, {state: {entry: 1}, replaceState: (_state, _title, url) => { scrubbed = true; assert.equal(url, pathname); }}, 1000);
    assert.equal(scrubbed, true); assert.equal(result?.link?.value, value);
  }
});

test('mailed query tokens and the reset-password alias are captured and scrubbed synchronously', () => {
  for (const pathname of ['/verify-email', '/reset', '/reset-password']) {
    const calls: unknown[][] = [];
    const capture = captureServicesLink({pathname, search: '?token=secret_123', hash: ''},
      {state: {entry: 1}, replaceState: (...args) => {calls.push(args);}}, 1000);
    assert.deepEqual(calls, [[{entry: 1}, '', pathname]]);
    assert.deepEqual(capture, {kind: pathname === '/verify-email' ? 'verify-email' : 'reset',
      link: {version: 1, kind: pathname === '/verify-email' ? 'verify-email' : 'reset', value: 'secret_123', createdAt: 1000}});
  }
});

test('ambiguous or malformed mailed query links are scrubbed and cannot revive a saved link', async () => {
  for (const [search, hash] of [
    ['?token=one&token=two', ''], ['?token=one&return=elsewhere', ''], ['?token=%00', ''],
    ['?token=', ''], ['?token=' + 'a'.repeat(4201), ''], ['?token=one', '#token=two'],
  ]) {
    const saved: ServicesLink = {version: 1, kind: 'verify-email', value: 'older', createdAt: 1000}, v = vault(saved);
    let scrubbed = false;
    const capture = captureServicesLink({pathname: '/verify-email', search, hash},
      {state: null, replaceState: (_state, _title, path) => {scrubbed = true; assert.equal(path, '/verify-email');}}, 1001);
    assert.equal(scrubbed, true);
    assert.equal(await restoreServicesLink(v.store, capture, 1001), undefined);
    assert.equal(v.value(), undefined);
  }
});

test('unknown continuation fragments are scrubbed and refused, while drawn Start anchors remain navigation', () => {
  for (const pathname of ['/', '/claim', '/account']) {
    let next: unknown;
    assert.equal(scrubUnsupportedServicesFragment({pathname, search: '', hash: '#unrecognized=private'}, {state: null, replaceState: (_state, _title, url) => { next = url; }}), true);
    assert.equal(next, pathname);
  }
  for (const hash of ['', '#join', '#setup']) assert.equal(scrubUnsupportedServicesFragment({pathname: '/', search: '', hash}, {state: null, replaceState() { assert.fail('navigation anchor changed'); }}), false);
});

test('ambiguous new links clear previous continuation rather than restoring it', async () => {
  const saved: ServicesLink = {version: 1, kind: 'device', value: '7KQM-4TXP', createdAt: 1000};
  for (const hash of ['#code=7KQM-4TXP&code=7KQM-4TXP', '#code=7KQM-4TXP&return=secret', '#code=ILLEGAL0', '#token=secret']) {
    const v = vault(saved);
    const capture = captureServicesLink({pathname: '/device', search: '', hash}, {state: null, replaceState() {}}, 1001);
    assert.equal(await restoreServicesLink(v.store, capture, 1001), undefined); assert.equal(v.value(), undefined);
  }
});

test('a protected continuation survives reload only within its drawn lifetime', async () => {
  const saved: ServicesLink = {version: 1, kind: 'device', value: '7KQM-4TXP', createdAt: 1000};
  const v = vault(saved);
  assert.deepEqual(await restoreServicesLink(v.store, undefined, 600999), saved);
  assert.equal(await restoreServicesLink(v.store, undefined, 601000), undefined);
  const future = vault({...saved, createdAt: 2000});
  assert.equal(await restoreServicesLink(future.store, undefined, 1000), undefined);
});

test('finishing an older page cannot clear a newer link opened in another tab', async () => {
  const old: ServicesLink = {version: 1, kind: 'reset', value: 'first', createdAt: 1000};
  const next = {...old, value: 'second', createdAt: 1001};
  const v = vault(next); await forgetServicesLink(v.store, old); assert.deepEqual(v.value(), next);
  await forgetServicesLink(v.store, next); assert.equal(v.value(), undefined);
});

test('a newly captured link never adopts a value read back from a racing page', async () => {
  const first: ServicesLink = {version: 1, kind: 'reset', value: 'first', createdAt: 1000};
  const second = {...first, value: 'second'};
  let reads = 0;
  const store: ServicesLinkStore = {change: async () => {}, read: async () => { reads++; return second; }};
  assert.deepEqual(await restoreServicesLink(store, {kind: 'reset', link: first}, 1001), first);
  assert.equal(await restoreServicesLink(store, {kind: 'reset'}, 1001), undefined);
  assert.equal(reads, 0);
});
