import assert from 'node:assert/strict';
import test from 'node:test';
import {parseSecurityReturnCode} from '../src/security-return.ts';

// ACC-LEAF-19: the registered-callback boundaries of parseSecurityReturnCode,
// authored UNRUN against the owner SDK checkpoint. The function carries no
// credentials and is not a sign-in consumer; these vectors pin exactly what it
// accepts from the callback URL and what it must refuse.

const state = Buffer.from(new Uint8Array(32).fill(7)).toString('base64url');
const otherState = Buffer.from(new Uint8Array(32).fill(9)).toString('base64url');
const code = 'scr_' + 'C'.repeat(43);
const transaction = 'oid_' + 'T'.repeat(43);
const origin = 'https://accounts.example.test';
const original = {origin, state};
const href = (query: string, base = origin, path = '/auth/portico-return') => base + path + '#' + query;
const canonical = () => 'security=' + code + '&state=' + state + '&transaction=' + transaction;

test('the canonical three-field producer spelling on the registered origin and path parses', () => {
  assert.deepEqual(parseSecurityReturnCode(href(canonical()), original), {code, state, transactionId: transaction});
});

test('another origin, path, state or an absent reference is refused', () => {
  for (const candidate of [
    href(canonical(), 'https://evil.example.test'),
    href(canonical(), 'http://accounts.example.test'),
    href(canonical(), origin, '/auth/portico-return/'),
    href(canonical(), origin, '/auth/portico-other'),
    href(canonical(), origin, '/'),
    href('security=' + code + '&state=' + otherState + '&transaction=' + transaction),
    href('security=' + code + '&transaction=' + transaction),
    origin + '/auth/portico-return',
    origin + '/auth/portico-return#',
    origin + '/auth/portico-return#security=' + code + '&state=' + state + '&transaction=oid_short',
    origin + '/auth/portico-return#security=scr_short&state=' + state + '&transaction=' + transaction,
    origin + '/auth/portico-return#security=' + 'C'.repeat(43) + '&state=' + state + '&transaction=' + transaction,
    origin + '/auth/portico-return#security=' + code + '&state=' + state + '&transaction=' + 'T'.repeat(43),
  ]) assert.throws(() => parseSecurityReturnCode(candidate, original), candidate);
});

test('duplicate, extra, reordered and percent-encoded aliases are refused', () => {
  for (const candidate of [
    href(canonical() + '&security=' + code),
    href(canonical() + '&extra=1'),
    href('state=' + state + '&security=' + code + '&transaction=' + transaction),
    href('security=' + code + '&state=' + state),
    // A real percent alias: it decodes to the same code while the fragment
    // spelling differs from the canonical producer string.
    href('security=%73cr_' + 'C'.repeat(43) + '&state=' + state + '&transaction=' + transaction),
  ]) assert.throws(() => parseSecurityReturnCode(candidate, original), candidate);
});

test('a query, userinfo or an unregistered original is refused before parsing', () => {
  for (const candidate of [
    origin + '/auth/portico-return?x=1#' + canonical(),
    'https://user@accounts.example.test/auth/portico-return#' + canonical(),
    origin + '/auth/portico-return#' + canonical() + '&',
  ]) assert.throws(() => parseSecurityReturnCode(candidate, original), candidate);
  for (const badOriginal of [
    {origin: 'https://accounts.example.test/path', state},
    {origin: 'http://accounts.example.test', state},
    {origin, state: 'short'},
    {origin, state: otherState.slice(0, 42)},
  ]) assert.throws(() => parseSecurityReturnCode(href(canonical()), badOriginal), badOriginal.origin + ' ' + badOriginal.state);
});

test('an oversized fragment is refused before the field checks', () => {
  assert.throws(() => parseSecurityReturnCode(href(canonical() + '&' + 'x=' + 'y'.repeat(2048)), original));
});
