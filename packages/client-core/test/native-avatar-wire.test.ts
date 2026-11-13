import assert from 'node:assert/strict';
import {Buffer} from 'node:buffer';
import test from 'node:test';
import {nativeAvatarWireBody} from '../src/native-avatar-wire.ts';

const own = 'https://original.test/v1/profiles/profile/avatar';
const managed = 'https://original.test/v1/profiles/profile/avatar?member=member';

test('own and managed avatar requests select exactly the original image bytes, including the full-size image', () => {
  for (const bytes of [new Uint8Array([0, 128, 255, 13, 10, 1]), new Uint8Array(4194304).fill(255)]) {
    const encoded = Buffer.from(bytes).toString('base64'), envelope = JSON.stringify({nativeAvatarBase64: encoded});
    for (const apple of [false, true]) for (const url of [own, managed]) {
      const body = nativeAvatarWireBody(url, 'PUT', envelope, apple);
      assert.equal(body, encoded);
      assert.deepEqual(Buffer.from(body!, 'base64'), Buffer.from(bytes));
    }
  }
});

test('JSON envelope shape and length refusals remain a native refusal, never a wire JSON fallback', () => {
  for (const apple of [false, true]) for (const body of [null, '', 'invalid', 'null', '[]', '12', '{}', '{"nativeAvatarBase64":12}',
    '{"nativeAvatarBase64":"AA==","extra":true}', '{"other":"AA=="}', ' '.repeat(5592449)]) {
    assert.equal(nativeAvatarWireBody(own, 'PUT', body, apple), null);
  }
});

test('non-avatar bodies, methods and URLs are forwarded without reconstruction', () => {
  const body = '{"nativeAvatarBase64":"AA=="}';
  for (const [url, method] of [[own, 'GET'], [own, 'POST'], ['https://original.test/v1/items/item', 'PUT'],
    ['https://original.test/v2/queue', 'PUT'], ['invalid URL', 'PUT']]) {
    assert.equal(nativeAvatarWireBody(url, method, body, false), body);
  }
});

test('providers without the new native codec retain their exact original bridge bodies', () => {
  for (const body of [null, '{"nativeAvatarBase64":"AA=="}', 'invalid', '{}']) {
    assert.equal(nativeAvatarWireBody(own, 'PUT', body, false, false), body);
    assert.equal(nativeAvatarWireBody(managed, 'PUT', body, true, false), body);
  }
});

test('Apple decoded paths and Android encoded paths keep their existing distinction; base64 validation stays native', () => {
  const body = '{"nativeAvatarBase64":"AA=="}', encoded = own.replace('/profile/', '/%70rofile/');
  assert.equal(nativeAvatarWireBody(encoded, 'PUT', body, true), 'AA==');
  assert.equal(nativeAvatarWireBody(encoded, 'PUT', body, false), body);
  assert.equal(nativeAvatarWireBody(own, 'PUT', '{"nativeAvatarBase64":""}', false), '');
  assert.equal(nativeAvatarWireBody(own, 'PUT', '{"nativeAvatarBase64":"\\nnot-base64"}', true), '\nnot-base64');
});
