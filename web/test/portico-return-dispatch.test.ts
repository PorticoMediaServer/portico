import test from 'node:test';
import assert from 'node:assert/strict';
import {capturePorticoReturn, dispatchPorticoReturn} from '../src/platform/portico-return-dispatch.ts';

const transaction = 'srt_' + 't'.repeat(43);
function capture(fragment: string, suffix = '') {
  const replaced: unknown[][] = [];
  const value = capturePorticoReturn({href: 'https://server.test/auth/portico-return' + suffix + fragment}, {replaceState: (...args) => {replaced.push(args);}});
  assert.deepEqual(replaced, [[null, '', '/auth/portico-return' + suffix]]);
  return value;
}
test('callback scrubs synchronously and only protected pending KIND selects the owner consumer', () => {
  const value = capture('#transaction=' + transaction);
  assert.equal(value.href, 'https://server.test/auth/portico-return');
  assert.deepEqual(dispatchPorticoReturn(value, {kind: 'owner', transactionId: transaction}), {kind: 'owner', transactionId: transaction, href: value.href + value.fragment});
  assert.throws(() => dispatchPorticoReturn(value, {kind: 'registered'}), {code: 'invalid_return'});
  const normal = capture('#code=src_' + 'c'.repeat(43) + '&state=' + 's'.repeat(43) + '&transaction=' + transaction);
  assert.equal(dispatchPorticoReturn(normal, {kind: 'registered'}).kind, 'registered');
  assert.throws(() => dispatchPorticoReturn(normal, {kind: 'owner', transactionId: transaction}), {code: 'invalid_return'});
});
for (const fragment of ['', '#transaction=x', '#transaction=' + transaction + '&transaction=' + transaction,
  '#transaction=' + transaction + '&state=s', '#transaction=' + transaction + '&purpose=switch', '#transaction=' + transaction + '&code=']) {
  test('owner callback refuses missing, duplicate or extra URL fields: ' + fragment.split('&').length, () => {
    assert.throws(() => dispatchPorticoReturn(capture(fragment), {kind: 'owner', transactionId: transaction}), {code: 'invalid_return'});
  });
}
test('query and another path are refused after the fragment is removed', () => {
  assert.throws(() => dispatchPorticoReturn(capture('#transaction=' + transaction, '?purpose=switch'), {kind: 'owner', transactionId: transaction}), {code: 'invalid_return'});
  const replaced: unknown[][] = [];
  const value = capturePorticoReturn({href: 'https://server.test/another#transaction=' + transaction}, {replaceState: (...args) => {replaced.push(args);}});
  assert.deepEqual(replaced, [[null, '', '/another']]);
  assert.throws(() => dispatchPorticoReturn(value, {kind: 'owner', transactionId: transaction}), {code: 'invalid_return'});
});

test('owner dispatch matches the actual protected opaque id without assigning it a normal-return prefix', () => {
  const value = capture('#transaction=actual-producer-id');
  assert.equal(dispatchPorticoReturn(value, {kind: 'owner', transactionId: 'actual-producer-id'}).kind, 'owner');
  assert.throws(() => dispatchPorticoReturn(value, {kind: 'owner', transactionId: 'successor-id'}), {code: 'invalid_return'});
});
for (const transactionId of ['x'.repeat(129), 'with space', 'has/slash', 'line\nbreak']) {
  test('owner dispatch keeps the canonical producer identifier grammar: ' + JSON.stringify(transactionId), () => {
    assert.throws(() => dispatchPorticoReturn(capture('#transaction=' + encodeURIComponent(transactionId)), {kind: 'owner', transactionId}), {code: 'invalid_return'});
  });
}

test('setup is a distinct protected kind and never inferred from callback fields',()=>{
 const value=capture('#transaction=actual-setup');
 assert.equal(dispatchPorticoReturn(value,{kind:'setup',transactionId:'actual-setup'}).kind,'setup');
 assert.throws(()=>dispatchPorticoReturn(value,{kind:'owner',transactionId:'other-owner'}),{code:'invalid_return'});
 assert.throws(()=>dispatchPorticoReturn(value,{kind:'registered'}),{code:'invalid_return'});
 assert.throws(()=>dispatchPorticoReturn(capture('#transaction=actual-setup&purpose=setup'),{kind:'setup',transactionId:'actual-setup'}),{code:'invalid_return'});
});
