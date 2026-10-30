import test from 'node:test';
import assert from 'node:assert/strict';
import {dispatchNativeAccountReturn} from '../src/platform/native-account-return.ts';

const origin = 'https://account.example', transaction = 'transaction_123', code = 'c'.repeat(43);
const href = origin + '/app/account-return#tx=' + transaction + '&code=' + code;
function fixture(value = href, expectedOrigin = origin) {
  const calls: unknown[][] = [];
  const location = {href: value, replace: (target: string | URL) => {calls.push(['open', String(target)]);}};
  const history = {replaceState: (...args: unknown[]) => {calls.push(['scrub', ...args]); location.href = new URL(String(args[2]), value).href;}};
  return {calls, location, history, run: () => dispatchNativeAccountReturn(location, history, expectedOrigin)};
}
test('the native return is scrubbed before the existing canonical app handoff, without returning its code', () => {
  const f = fixture();
  assert.equal(f.run(), 'forwarded');
  assert.deepEqual(f.calls, [['scrub', null, '', '/app/account-return'], ['open', 'portico://account-return?transactionId=' + transaction + '&code=' + code]]);
  assert.equal(f.location.href, origin + '/app/account-return');
  assert.equal(f.run(), 'invalid', 'a second bootstrap sees no completion fragment to replay');
  assert.equal(f.calls.filter(call => call[0] === 'open').length, 1);
});
test('another Services route is left to its own dispatcher, including its fragment', () => {
  const f = fixture(origin + '/join#code=not-a-native-completion');
  assert.equal(f.run(), undefined); assert.deepEqual(f.calls, []);
  assert.equal(f.location.href, origin + '/join#code=not-a-native-completion');
});
for (const [name, value] of [
  ['missing completion', origin + '/app/account-return#tx=' + transaction],
  ['short code', origin + '/app/account-return#tx=' + transaction + '&code=short'],
  ['extra field', href + '&purpose=switch'],
  ['duplicate code', href + '&code=' + code],
  ['duplicate transaction', href + '&tx=' + transaction],
  ['query', origin + '/app/account-return?returnTo=https://other.example#tx=' + transaction + '&code=' + code],
  ['other origin', href.replace(origin, 'https://other.example')],
  ['user information', href.replace('https://', 'https://name@')],
  ['oversize', href + '&extra=' + 'x'.repeat(1024)],
]) test('invalid native return forwards nothing after scrubbing: ' + name, () => {
  const f = fixture(value); assert.equal(f.run(), 'invalid');
  assert.equal(f.calls.length, 1); assert.equal(f.calls[0][0], 'scrub'); assert.equal(new URL(f.location.href).hash, '');
});
for (const expectedOrigin of ['https://account.example/', 'https://account.example/path', 'https://name@account.example', 'https://account.example#fragment', 'http://account.example']) {
  test('the configured origin must be exact and use the existing account transport policy: ' + expectedOrigin, () => {
    const f = fixture(href, expectedOrigin); assert.equal(f.run(), 'invalid'); assert.equal(f.calls.length, 1);
  });
}
test('the existing explicit loopback account development origin remains supported', () => {
  const local = 'http://127.0.0.1:19410', f = fixture(href.replace(origin, local), local);
  assert.equal(f.run(), 'forwarded'); assert.equal(f.calls[1][1], 'portico://account-return?transactionId=' + transaction + '&code=' + code);
});
test('a failed scrub cannot open the app with an unsanitized address bar', () => {
  const f = fixture(); const result = dispatchNativeAccountReturn(f.location, {replaceState: () => {throw new Error('history denied');}}, origin);
  assert.equal(result, 'invalid'); assert.deepEqual(f.calls, []);
});
test('an unavailable app navigation returns invalid after the fragment has been removed', () => {
  const f = fixture(); f.location.replace = () => {assert.equal(new URL(f.location.href).hash, ''); throw new Error('app unavailable');};
  assert.equal(f.run(), 'invalid'); assert.deepEqual(f.calls, [['scrub', null, '', '/app/account-return']]);
});
