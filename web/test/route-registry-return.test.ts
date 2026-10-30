import test from 'node:test';
import assert from 'node:assert/strict';
import {captureWebAccountReturn, accountReturnPath} from '../src/route-registry-return.ts';
import {webScreenIdForRoute} from '../src/route-registry.ts';
import {dispatchPorticoReturn} from '../src/platform/portico-return-dispatch.ts';

test('the fixed return is scrubbed synchronously and delivered only once in memory', () => {
  const events: string[] = [], page = {href: 'https://server.example/auth/portico-return#code=private-code&state=private-state&transaction=private-tx'};
  const take = captureWebAccountReturn(page, {replaceState: (_state, _unused, href) => {events.push('scrub');page.href = new URL(String(href), page.href).href;}});
  events.push('startup');assert.deepEqual(events, ['scrub', 'startup']);assert.equal(page.href, 'https://server.example/auth/portico-return');
  assert.deepEqual(take?.(), {href: page.href, fragment: '#code=private-code&state=private-state&transaction=private-tx'});assert.equal(take?.(), undefined);
  assert.equal(webScreenIdForRoute(accountReturnPath), 'signIn');
});
test('other paths never claim or scrub an unrelated fragment', () => {
  for (const path of ['/sign-in', '/auth/portico-return/extra', '/auth/portico-return/']) {
    assert.equal(captureWebAccountReturn({href: 'https://server.example' + path + '#private'}, {replaceState: assert.fail}), undefined);
  }
});
test('scrubbing does not infer owner authority from a transaction-only or malformed fragment', () => {
  for (const fragment of ['#transaction=owner-transaction', '#purpose=switch&transaction=owner-transaction', '#code=malformed', '']) {
    let scrubbed = false;
    const take = captureWebAccountReturn({href: 'https://server.example/auth/portico-return' + fragment}, {replaceState: () => {scrubbed = true;}});
    assert.equal(scrubbed, true);assert.throws(() => dispatchPorticoReturn(take!()!, {kind: 'registered'}));
  }
});
test('failure to scrub stops startup instead of delivering a still-exposed callback', () => {
  assert.throws(() => captureWebAccountReturn({href: 'https://server.example/auth/portico-return#private'}, {replaceState: () => {throw new Error('fixture-history-failure');}}), /fixture-history-failure/);
});
