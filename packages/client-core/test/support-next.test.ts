import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
import {readOwnerSupport, fixOwnerSupportPermissions, prepareOwnerSupportExport} from '../src/server-admin/support-next.ts';

test('Support reads the rebuilt states and keeps an unfixable permissions warning truthful', async () => {
  const calls: unknown[] = [], permissions = {path: '/data', owner: 'owner', account: 'portico', readableByOthers: true, fixable: false, checked: true,
    reason: 'not_kept_here', message: 'Move the data folder to a drive that keeps permissions.'};
  const capabilities = {items: [{area: 'graphics', name: 'qsv', state: 'checking'}, {area: 'folders', name: 'recordings', state: 'not_used'}]};
  const api = {request: async <T>(path: string, method?: string): Promise<T> => {calls.push([path, method]); return (path.endsWith('/capabilities') ? capabilities : permissions) as T;}};
  const facts = await readOwnerSupport(api);
  assert.deepEqual(facts.capabilities.map(c => c.state), ['checking', 'not_used']);
  assert.equal(facts.permissions.readableByOthers, true);
  assert.equal(facts.permissions.fixable, false);
  assert.equal(facts.permissions.reason, 'not_kept_here');
  assert.deepEqual(await fixOwnerSupportPermissions(api), permissions);
  assert.deepEqual(calls, [['/v1/admin/capabilities', 'GET'], ['/v1/admin/data-folder/permissions', 'GET'], ['/v1/admin/data-folder/permissions/fix', 'POST']]);
});

test('Support keeps the actual ZIP bytes and sends the captured choices under the selected session', async () => {
  const bytes = new Uint8Array([80, 75, 3, 4, 1, 2]);
  let request!: RequestInit, target = '';
  const api = new HttpLocalApi('http://127.0.0.1:19447', 'fixture-token', async (input, init) => {
    target = String(input); request = init!;
    return new Response(bytes, {headers: {'Content-Type': 'application/zip'}});
  });
  const result = await prepareOwnerSupportExport(api, {windowMinutes: 60, components: ['runtime', 'audit'], includePaths: false});
  assert.equal(target, 'http://127.0.0.1:19447/v1/admin/support-export');
  assert.equal(request.method, 'POST');
  assert.equal(new Headers(request.headers).get('Authorization'), 'Bearer fixture-token');
  assert.equal(new Headers(request.headers).get('Accept'), 'application/zip');
  assert.deepEqual(JSON.parse(String(request.body)), {windowMinutes: 60, components: ['runtime', 'audit'], includePaths: false});
  assert.deepEqual(new Uint8Array(await result.blob.arrayBuffer()), bytes);
  assert.equal(result.filename, 'portico-support.zip');
  assert.equal('expiresAt' in result, false);
});

test('Support refuses a non-ZIP answer or a scope cancelled while the ZIP is being read', async () => {
  const options = {windowMinutes: 60, components: ['snapshot'], includePaths: false} as const;
  const wrong = new HttpLocalApi('http://127.0.0.1:19447', 'fixture-token', async () => new Response('{}', {headers: {'Content-Type': 'application/json'}}));
  await assert.rejects(prepareOwnerSupportExport(wrong, options));
  const controller = new AbortController();
  const api = new HttpLocalApi('http://127.0.0.1:19447', 'fixture-token', async () => {
    const response = new Response('zip', {headers: {'Content-Type': 'application/zip'}});
    response.blob = async () => {controller.abort(); return new Blob(['zip']);};
    return response;
  });
  await assert.rejects(prepareOwnerSupportExport(api, options, controller.signal), {name: 'AbortError'});
});
