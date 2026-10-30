import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';

test('only an opening preparation may omit the original Detail capture', () => {
  const opening = {variant: 'preparing', kind: 'file-info-preparing', state: 'preparing', queryId: `open.${'a'.repeat(43)}`, retryAfterMs: 1000};
  for (const response of ['GetItemsIdResponse', 'GetItemsIdDetailResponse', 'GetFileInfoQueryResponse']) {
    assert.doesNotThrow(() => decode(response, opening), response);
  }
  const detail = {scope: {serverId: 'server', libraryId: 'library', itemId: 'item', viewerFence: 'original-detail'}, revision: {catalog: 71, viewer: 8}};
  const lane = {...opening, queryId: `${'a'.repeat(183)}.${'b'.repeat(154)}`, detail};
  for (const response of ['GetItemsIdFileInfoResponse', 'GetItemFileInfoGroupsResponse', 'GetItemFileInfoMembersResponse', 'GetItemFileInfoTracksResponse', 'GetItemFileInfoHeadTracksResponse', 'GetItemFileInfoSourcesResponse', 'GetItemFileInfoDiscTitlesResponse']) {
    const decoded = decode(response, lane) as typeof lane;
    assert.deepEqual(JSON.parse(JSON.stringify(decoded.detail)), detail, response);
    assert.throws(() => decode(response, {...lane, detail: undefined}), response);
  }
  // Responses are open: an unknown member is dropped and an unknown variant decodes to the typed unknown value.
  assert.equal('futureField' in (decode('GetItemsIdResponse', {...opening, futureField: 1}) as object), false);
  assert.deepEqual({...(decode('GetItemsIdResponse', {variant: 'from-the-future', x: 1}) as object)}, {variant: 'from-the-future', unknownVariant: true});
  assert.deepEqual(JSON.parse(JSON.stringify((decode('GetFileInfoQueryResponse', lane) as typeof lane).detail)), detail);
});
