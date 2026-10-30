/** Cast is offered only where the server names the receiver application this build was made for. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {castOffered} from '../src-next/platform/play-on/cast-offered.ts';

test('the server’s setting decides: none set, no Cast', () => {
  assert.equal(castOffered(true, '', 'ABCD1234'), false);
  assert.equal(castOffered(true, undefined, 'ABCD1234'), false);
  assert.equal(castOffered(true, 42, 'ABCD1234'), false);
});

test('the application this build was made for is the only one it can start', () => {
  assert.equal(castOffered(true, 'ABCD1234', 'ABCD1234'), true);
  assert.equal(castOffered(true, 'abcd1234', 'ABCD1234'), true);
  assert.equal(castOffered(true, 'FFFF0000', 'ABCD1234'), false);
});

test('a build that cannot cast offers nothing, and one that cannot name its application trusts the server', () => {
  assert.equal(castOffered(false, 'ABCD1234', 'ABCD1234'), false);
  assert.equal(castOffered(true, 'ABCD1234', undefined), true);
  assert.equal(castOffered(true, 'ABCD1234', ''), true);
});
