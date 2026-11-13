import test from 'node:test';
import assert from 'node:assert/strict';
import {createStorageForms} from '../src-next/screens/settings-live/server/storage/forms.ts';

test('native Intros carries its read version through the screen wrapper when saving', async () => {
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: '1', viewerFence: 'fence'};
  const writes: {path: string; method: string; options: any}[] = [];
  const doc = {name: 'segments', etag: '"7"', values: {viewerConfidencePercent: 80, automaticConfidencePercent: 95}};
  const api = {
    request: async <T>() => doc as T,
    requestRaw: async (path: string, method: string, options: any) => {
      writes.push({path, method, options});
      return {status: 200, headers: {}, body: {...doc, etag: '"8"', values: {...doc.values, ...options.body}}};
    },
  };
  const context = {api, current: () => true, authority: () => authority, subscribeAuthority: () => () => {},
    account: {serverId: 'server', account: {id: 'owner', role: 'owner', disabled: false, primaryProfileId: 'main'}, capabilities: {manageServer: true}}};
  const group = createStorageForms(context as never, ['segments'], error => {throw error;});
  try {
    await group.forms.segments.load();
    group.forms.segments.set({viewerConfidencePercent: 75});
    assert.equal(await group.set.save(), true);
    assert.equal(writes.length, 1);
    assert.equal(writes[0].path, '/v1/admin/settings/segments');
    assert.equal(writes[0].method, 'PUT');
    assert.deepEqual(writes[0].options.headers, {'If-Match': '"7"'});
    assert.deepEqual(writes[0].options.body, {viewerConfidencePercent: 75});
  } finally {group.dispose();}
});
