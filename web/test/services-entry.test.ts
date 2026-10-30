import assert from 'node:assert/strict';
import {test} from 'node:test';
import {captureServicesEntry} from '../src/services/entry.ts';

const origin = 'https://account.example';
function nativePage(fragment: string) {
  const calls: unknown[][] = [];
  const page = {href: origin + '/app/account-return' + fragment, replace: (value: string | URL) => { calls.push(['open', String(value)]); },
    get pathname(): string { throw new Error('generic fragment handling ran'); },
    get search(): string { throw new Error('generic fragment handling ran'); },
    get hash(): string { throw new Error('generic fragment handling ran'); }};
  const history = {state: null, replaceState: (...args: unknown[]) => { calls.push(['scrub', ...args]); }};
  return {page, history, calls};
}
test('Services gives the checked Apps native return exclusive synchronous dispatch before generic capture', () => {
  const code = 'c'.repeat(43), f = nativePage('#tx=transaction_123&code=' + code);
  assert.deepEqual(captureServicesEntry(f.page, f.history, origin), {native: 'forwarded'});
  assert.deepEqual(f.calls.map(call => call[0]), ['scrub', 'open']);
});
test('a refused native return goes only to the caller fallback and never to generic capture', () => {
  const f = nativePage('#tx=transaction_123&code=short');
  assert.deepEqual(captureServicesEntry(f.page, f.history, origin), {native: 'invalid'});
  assert.deepEqual(f.calls.map(call => call[0]), ['scrub']);
});
test('ordinary device intake is scrubbed before Services can start storage or account work', () => {
  const page = {href: origin + '/device#code=7KQM4TXP', pathname: '/device', hash: '#code=7KQM4TXP', search: '', replace() { throw new Error('no app handoff'); }};
  const calls: unknown[][] = [];
  const history = {state: null, replaceState: (...args: unknown[]) => { calls.push(args); page.hash = ''; }};
  const result = captureServicesEntry(page, history, origin);
  assert.equal(result.capture?.link?.value, '7KQM-4TXP');
  assert.equal(result.invalidContinuation, false);
  assert.equal(page.hash, ''); assert.equal(calls.length, 1);
});

test('a canonical invitation is scrubbed and retained only in this document', () => {
  const code = 'a'.repeat(32), href = 'https://web.getportico.tv/join#code=' + code + '&server=https%3A%2F%2Fserver.example';
  const page = {href, pathname: '/join', hash: new URL(href).hash, search: '', replace() { throw new Error('no app handoff'); }};
  const history: {state: unknown; replaceState: (_state: unknown, _unused: string, path?: string | URL | null) => void} = {state: null, replaceState: (state, _unused, path) => { history.state = state; page.href = new URL(String(path), page.href).href; page.hash = ''; }};
  const result = captureServicesEntry(page, history, 'https://web.getportico.tv');
  assert.deepEqual(result.join, {code, server: 'https://server.example'});
  assert.equal(result.invalidContinuation, false); assert.equal(page.hash, '');
  assert.equal(captureServicesEntry(page, history, 'https://web.getportico.tv').invalidContinuation, true, 'a document opened after the scrub must require the invitation again');
});

test('a plain /join opens the typed short entry only before any capability attempt', () => {
  for (const pathname of ['/join', '/join/']) {
    const page = {href: origin + pathname, pathname, hash: '', search: '', replace() {}};
    let scrubbed = 0;
    const history = {state: null, replaceState: () => {scrubbed++;}};
    assert.deepEqual(captureServicesEntry(page, history, origin), {plainShortJoin: true, invalidContinuation: false});
    assert.deepEqual(captureServicesEntry(page, history, origin), {plainShortJoin: true, invalidContinuation: false});
    assert.equal(scrubbed, 0);
  }
  for (const suffix of ['?', '#']) {
    const page = {href: origin + '/join' + suffix, pathname: '/join', hash: '', search: '', replace() {}};
    assert.deepEqual(captureServicesEntry(page, {state: null, replaceState() {}}, origin), {join: undefined, invalidContinuation: true});
  }
});

test('a malformed or scrubbed /join never falls back to the typed entry', () => {
  const href = origin + '/join#code=nope', page = {href, pathname: '/join', hash: '#code=nope', search: '', replace() {}};
  const history: {state: unknown; replaceState: (_state: unknown, _unused: string, path?: string | URL | null) => void} = {state: null, replaceState: (state, _unused, path) => {history.state = state;page.href = new URL(String(path), page.href).href;page.hash = '';}};
  const first = captureServicesEntry(page, history, origin);
  assert.equal(first.plainShortJoin, undefined); assert.equal(first.join, undefined); assert.equal(first.invalidContinuation, true);
  const second = captureServicesEntry(page, history, origin);
  assert.equal(second.plainShortJoin, undefined); assert.equal(second.invalidContinuation, true);
  const query = {href: origin + '/join?next=1', pathname: '/join', hash: '', search: '?next=1', replace() {}};
  assert.deepEqual(captureServicesEntry(query, {state: null, replaceState() {}}, origin), {join: undefined, invalidContinuation: true});
});

test('provider failures are scrubbed into a bounded display hint before any saved continuation is read', () => {
  for(const [path,value,expected] of [['/','google','google'],['/','apple','apple'],['/account','1','unknown']] as const){
    const page={href:origin+path+'?identityError='+value,pathname:path,search:'?identityError='+value,hash:'',replace(){throw new Error('unexpected redirect');}};
    let target:unknown;
    const result=captureServicesEntry(page,{state:null,replaceState:(_s,_u,url)=>{target=url;}},origin);
    assert.equal(result.providerFailure,expected);assert.equal(target,path === '/account' ? '/account?tab=security' : '/');
    assert.equal(result.signIn,undefined);assert.equal(result.capture,undefined);
  }
});
