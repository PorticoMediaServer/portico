import test from 'node:test';
import assert from 'node:assert/strict';
import {accountMoveLink} from '@core/account-move.ts';
import {captureWebAccountArrival} from '../src/route-registry-arrival.ts';

const code = 'A'.repeat(43), id = 'srv_' + 'A'.repeat(43);
function browser(href: string) {
  let page = new URL(href);
  const events: string[] = [];
  return {
    location: {get href() {return page.href;}}, events,
    history: {replaceState: (_state: unknown, _unused: string, value?: string | URL | null) => {
      events.push('scrub');page = new URL(String(value), page);
    }},
  };
}

test('move capture scrubs a live Location before startup and delivers the original expected-ID link once', () => {
  const b = browser(accountMoveLink('https://server.example', code, id));
  const arrival = captureWebAccountArrival(b.location, b.history);
  b.events.push('startup');
  assert.deepEqual(b.events, ['scrub', 'startup']);
  assert.equal(b.location.href, 'https://server.example/sign-in');
  assert.equal(arrival.takeAccountReturn, undefined);
  const link = arrival.takeAccountMove?.();
  assert.deepEqual(link, {code, id});assert.ok(Object.isFrozen(link));
  assert.equal(arrival.takeAccountMove?.(), undefined);
});
test('legacy code-only move remains an original code-only link without inventing an expected ID', () => {
  const b = browser(accountMoveLink('https://server.example', code));
  const arrival = captureWebAccountArrival(b.location, b.history);
  assert.deepEqual(arrival.takeAccountMove?.(), {code});
  assert.equal(arrival.takeAccountMove?.(), undefined);
});
test('the fixed Portico callback remains first and exclusive even when it contains a code or move-looking fields', () => {
  const b = browser(`https://server.example/auth/portico-return#code=${code}&id=${id}`);
  const arrival = captureWebAccountArrival(b.location, b.history);
  assert.equal(arrival.takeAccountMove, undefined);
  assert.equal(b.location.href, 'https://server.example/auth/portico-return');
  assert.deepEqual(arrival.takeAccountReturn?.(), {href: b.location.href, fragment: `#code=${code}&id=${id}`});
  assert.equal(arrival.takeAccountReturn?.(), undefined);
});
test('ordinary sign-in and unrelated routes do not acquire move state or scrub their URL', () => {
  for (const href of ['https://server.example/sign-in', 'https://server.example/sign-in?from=profile',
    `https://server.example/profiles#code=${code}`, `https://server.example/sign-in/other#code=${code}`]) {
    const b = browser(href), arrival = captureWebAccountArrival(b.location, b.history);
    assert.equal(arrival.takeAccountMove, undefined);assert.equal(arrival.takeAccountReturn, undefined);
    assert.deepEqual(b.events, []);assert.equal(b.location.href, href);
  }
});
test('invalid move fragments are scrubbed synchronously then rejected once without startup failure or stripped-ID fallback', () => {
  for (const suffix of [`#code=${code}&id=bad`, `#code=${code}&code=${code}`, `#code=${code}&id=${id}&id=${id}`,
    `#code=${code}&extra=value`, `?code=${code}#code=${code}`, '#transaction=not-a-move']) {
    const b = browser('https://server.example/sign-in' + suffix);
    const arrival = captureWebAccountArrival(b.location, b.history);
    assert.deepEqual(b.events, ['scrub']);assert.equal(b.location.href, 'https://server.example/sign-in');
    assert.equal(typeof arrival.takeAccountMove, 'function');
    assert.throws(() => arrival.takeAccountMove?.());
    assert.equal(arrival.takeAccountMove?.(), undefined);
  }
});
test('failed history scrub aborts capture synchronously before installation or protected storage can start', () => {
  const failure = new Error('History unavailable'), events: string[] = [];
  for (const fragment of [`#code=${code}&id=${id}`, '#code=invalid']) {
    assert.throws(() => {
      captureWebAccountArrival({href: 'https://server.example/sign-in' + fragment}, {
        replaceState: () => {events.push('scrub-failed');throw failure;},
      });
      events.push('startup');
    }, error => error === failure);
  }
  assert.deepEqual(events, ['scrub-failed', 'scrub-failed']);
});
test('teardown discards valid and invalid unused arrivals without delivering or throwing', () => {
  for (const suffix of [`#code=${code}&id=${id}`, '#code=invalid']) {
    const b = browser('https://server.example/sign-in' + suffix);
    const arrival = captureWebAccountArrival(b.location, b.history);
    arrival.dispose();arrival.dispose();
    assert.equal(arrival.takeAccountMove?.(), undefined);
  }
  const b = browser('https://server.example/auth/portico-return#transaction=original');
  const arrival = captureWebAccountArrival(b.location, b.history);
  arrival.dispose();assert.equal(arrival.takeAccountReturn?.(), undefined);
});
