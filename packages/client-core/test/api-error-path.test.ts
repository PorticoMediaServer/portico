import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, HttpLocalApi, errorFieldPath} from '../src/index.ts';

test('ApiError keeps the server-named field path as a machine field', async () => {
  const reply = (error: Record<string, unknown>) => async () => new Response(JSON.stringify({error}), {status: 400, headers: {'Content-Type': 'application/json'}});
  const api = new HttpLocalApi('http://127.0.0.1:19447', 'token', reply({code: 'invalid_library_channel', message: 'Choose at least one library.', path: 'rules[0].query.libraryIds'}));
  const e = await api.request('/v1/admin/library-channels', 'POST', {}).catch(x => x);
  assert.ok(e instanceof ApiError);
  assert.equal(e.code, 'invalid_library_channel');
  assert.equal(e.path, 'rules[0].query.libraryIds');
  const none = await new HttpLocalApi('http://127.0.0.1:19447', 'token', reply({code: 'x', path: '<b>not a path</b>'})).request('/v1/x').catch(x => x);
  assert.equal(none.path, undefined);
});

test('errorFieldPath accepts plain field paths only', () => {
  for (const ok of ['name', 'rules[0].query.kinds', 'timezone', 'rules[12].query.includeItemIds']) assert.equal(errorFieldPath(ok), ok);
  for (const bad of ['', '0name', 'a..b', 'a[x]', 'a b', 'a.' + 'b'.repeat(300), 42, null, undefined]) assert.equal(errorFieldPath(bad), undefined, String(bad));
});
