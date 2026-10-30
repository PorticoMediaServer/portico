import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, HttpLocalApi, errorConfirmRoots} from '../src/index.ts';
import {lanConfirmationRoots, remoteSourceDraft, withConfirmedLanRoots} from '../src/linear-api.ts';

const refused = (roots: unknown) => new Response(JSON.stringify({error: {code: 'source_lan_confirmation_required', message: 'Confirm the local-network roots.', retry: 'none', current: {confirmRoots: roots}}}), {status: 422, headers: {'Content-Type': 'application/json'}});

test('CD-06: a 422 source_lan_confirmation_required keeps its confirmRoots on the ApiError', async () => {
  const api = new HttpLocalApi('https://server.example', 'token', (async () => refused(['http://192.168.1.50', 'http://192.168.1.50:5004'])) as typeof fetch);
  const error = await api.request('/v1/admin/live-sources/remote/preview', 'POST', {}).catch(e => e);
  assert.ok(error instanceof ApiError);
  assert.equal(error.status, 422);
  assert.deepEqual(lanConfirmationRoots(error), ['http://192.168.1.50', 'http://192.168.1.50:5004']);
});

test('roots are plain http(s) URLs in a short list, or nothing', () => {
  assert.equal(errorConfirmRoots(['ftp://x']), undefined);
  assert.equal(errorConfirmRoots(['http://user:pass@x']), undefined);
  assert.equal(errorConfirmRoots([]), undefined);
  assert.equal(errorConfirmRoots(Array.from({length: 17}, (_, i) => `http://10.0.0.${i}`)), undefined);
  assert.equal(lanConfirmationRoots(new ApiError(422, 'invalid_request', 'x')), undefined, 'another code is not a confirmation');
});

test('each re-preview carries every root confirmed so far, once each (a second device can be reported)', () => {
  let draft = remoteSourceDraft('rq');
  draft = withConfirmedLanRoots(draft, ['http://192.168.1.50']);
  draft = withConfirmedLanRoots(draft, ['http://192.168.1.50', 'http://192.168.1.60:5004']);
  assert.deepEqual(draft.confirmedLanRoots, ['http://192.168.1.50', 'http://192.168.1.60:5004']);
});
