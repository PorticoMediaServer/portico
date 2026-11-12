import test from 'node:test';
import assert from 'node:assert/strict';
import {partError} from '../src/screens/settings/server/troubleshooting/index.tsx';

test('a Troubleshooting part keeps its own failure; only a signed-out answer or a lost role reaches the page', () => {
  const page: unknown[] = [];
  const own = partError(error => page.push(error));
  for (const error of [{status: 404, code: 'not_found'}, {status: 500}, new Error('network'), undefined]) own(error);
  assert.deepEqual(page, [], 'a missing route, a server error or a lost connection fails that part only');
  own({status: 401}); own({status: 403, code: 'forbidden'});
  assert.equal(page.length, 2);
});
