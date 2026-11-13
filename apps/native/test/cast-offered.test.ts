/** Cast is offered only where the server names the receiver application this build was made for. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {castOffered} from '../src-next/platform/play-on/cast-offered.ts';

test('an empty server build id offers no Cast', () => {
  assert.equal(castOffered(true, '', 'ABCD1234'), false);
  assert.equal(castOffered(true, undefined, 'ABCD1234'), false);
  assert.equal(castOffered(true, 42, 'ABCD1234'), false);
});

test('the application this build was made for is the only one it can start', () => {
  assert.equal(castOffered(true, 'ABCD1234', 'ABCD1234'), true);
  assert.equal(castOffered(true, 'abcd1234', 'ABCD1234'), true);
  assert.equal(castOffered(true, 'FFFF0000', 'ABCD1234'), false);
});

test('an empty native build id cannot silently use another registered receiver', () => {
  assert.equal(castOffered(false, 'ABCD1234', 'ABCD1234'), false);
  assert.equal(castOffered(true, 'ABCD1234', undefined), false);
  assert.equal(castOffered(true, 'ABCD1234', ''), false);
});
