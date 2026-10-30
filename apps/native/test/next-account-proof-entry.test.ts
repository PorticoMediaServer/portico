import test from 'node:test';
import assert from 'node:assert/strict';
import {accountProofFromURL} from '../src-next/dev/account-proof-entry.ts';

test('DEV account proof launch carries only the typed server address into genuine sign-in', () => {
  assert.deepEqual(accountProofFromURL('portico://account-proof?server=http%3A%2F%2F100.120.50.24%3A32743'), {serverAddress: 'http://100.120.50.24:32743'});
  assert.deepEqual(accountProofFromURL('portico://account-proof?server=https%3A%2F%2FMedia.Example.com'), {serverAddress: 'https://Media.Example.com'});
});
test('credentials, injected state, malformed addresses and other launch routes are refused', () => {
  for (const value of [null, '', 'portico://kit?gallery=home', 'https://account-proof?server=https://media.example',
    'portico://account-proof/path?server=https://media.example', 'portico://account-proof?server=https://media.example#accessToken=secret',
    'portico://account-proof?server=https://media.example&runtime=fake', 'portico://account-proof?server=https://media.example&password=secret',
    'portico://account-proof?server=https://media.example&server=https://other.example', 'portico://account-proof?server=https://user:secret@media.example',
    'portico://account-proof?server=https://media.example/v1/system', 'portico://account-proof?server=javascript:alert(1)']) {
    assert.equal(accountProofFromURL(value), undefined);
  }
});
