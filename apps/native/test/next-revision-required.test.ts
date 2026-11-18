import test from 'node:test';
import assert from 'node:assert/strict';
import {revisionRequiredFetch} from '../src-next/platform/revision-required-fetch.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';

const origin = 'http://server.test';
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), {status, headers: {'Content-Type': 'application/json'}});
const required = () => json({error: {code: 'revision_required', retry: 'never'}}, 428);

test('queue and session commands reread their parent and retry using If-Match without changing the intent', async () => {
  for (const [method, path, readPath] of [
    ['DELETE', routePath('remove_queue_entry', {id: 'queue/one', entryId: 'entry/one'}), routePath('get_queue', {id: 'queue/one'})],
    ['PATCH', routePath('change_playback_session', {id: 'session/one'}), routePath('get_playback_session', {id: 'session/one'})],
  ]) {
    let count = 0;
    const send = revisionRequiredFetch(async (input, options = {}) => {
      count++;
      const headers = new Headers(options.headers);
      assert.equal(headers.get('Authorization'), 'Bearer viewer');
      if (count === 1) return required();
      if (options.method === 'GET') {
        assert.equal(input, origin + readPath);
        assert.equal(options.body, undefined);
        assert.equal(headers.get('If-Match'), null);
        assert.equal(headers.get('Idempotency-Key'), null);
        return json({revision: '9223372036854775807'});
      }
      assert.equal(headers.get('If-Match'), '9223372036854775807');
      assert.equal(headers.get('Idempotency-Key'), 'same-operation');
      assert.equal(options.body, '{"state":"paused"}');
      return json({saved: true});
    });
    assert.equal((await send(origin + path!, {method, headers: {Authorization: 'Bearer viewer', 'Idempotency-Key': 'same-operation'}, body: '{"state":"paused"}'})).status, 200);
    assert.equal(count, 3);
  }
});

test('the retired device trust mutation is never reread or replayed', async () => {
  let calls = 0;
  const refusal = required();
  const options = {method: 'PUT', body: '{"trust":"blocked"}'};
  const send = revisionRequiredFetch(async (input, actual) => {
    calls++;
    assert.equal(input, origin + '/v1/admin/access/devices/device%2Fone/trust');
    assert.equal(actual, options);
    return refusal;
  });
  assert.equal(await send(origin + '/v1/admin/access/devices/device%2Fone/trust', options), refusal);
  assert.equal(calls, 1);
});

test('revision retries remain bounded and preserve the last refusal for the shared error presenter', async () => {
  let reads = 0, writes = 0;
  const send = revisionRequiredFetch(async (_input, options = {}) => {
    if (options.method === 'GET') { reads++; return json({revision: reads}); }
    writes++; return required();
  });
  const response = await send(origin + routePath('put_metadata_settings'), {method: 'PUT', body: '{}'});
  assert.equal(response.status, 428);
  assert.equal((await response.json()).error.code, 'revision_required');
  assert.equal(reads, 2); assert.equal(writes, 3);
});

test('a failed or unreadable reread never replays the mutation', async () => {
  for (const reply of [json({}, 403), json({revision: -1}), json({revision: 'not-a-revision'}), json({revision: 9}, 503)]) {
    let calls = 0;
    const send = revisionRequiredFetch(async () => ++calls === 1 ? required() : reply);
    assert.equal((await send(origin + routePath('put_metadata_settings'), {method: 'PUT', body: '{}'})).status, 428);
    assert.equal(calls, 2);
  }
});

test('cancelling the reread never sends another mutation', async () => {
  const abort = new AbortController(); let calls = 0;
  const send = revisionRequiredFetch(async (_input, options = {}) => {
    calls++;
    if (calls === 1) return required();
    assert.equal(options.signal, abort.signal);
    abort.abort();
    return json({revision: 5});
  });
  assert.equal((await send(origin + routePath('put_metadata_settings'), {method: 'PUT', body: '{}', signal: abort.signal})).status, 428);
  assert.equal(calls, 2);
});

test('other 428 reasons, stale revisions, authentication and interrupted writes retain their own policy', async () => {
  for (const status of [401, 403, 409, 412, 500]) {
    let calls = 0;
    const response = json({error: {code: 'revision_required'}}, status);
    const send = revisionRequiredFetch(async () => { calls++; return response; });
    assert.equal(await send(origin + routePath('put_metadata_settings'), {method: 'PUT', body: '{}'}), response);
    assert.equal(calls, 1);
  }
  let calls = 0;
  const send = revisionRequiredFetch(async () => { calls++; return json({error: {code: 'idempotency_key_required'}}, 428); });
  assert.equal((await (await send(origin + routePath('put_metadata_settings'), {method: 'PUT', body: '{}'})).json()).error.code, 'idempotency_key_required');
  assert.equal(calls, 1);
  const offline = new Error('offline');
  await assert.rejects(revisionRequiredFetch(async () => { throw offline; })(origin + routePath('put_metadata_settings'), {method: 'PUT', body: '{}'}), e => e === offline);
});
