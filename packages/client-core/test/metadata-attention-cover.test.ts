import test from 'node:test';
import assert from 'node:assert/strict';
import {parseMetadataAttention} from '../src/metadata-attention.ts';

const digest = 'a'.repeat(64);
function page(kind: 'album' | 'song', artworkUrl: string) {
  return {libraryId: 'music', total: 1, nextCursor: '', entries: [{kind, entityId: kind === 'album' ? 'album' : 'song', parentId: kind === 'song' ? 'album' : '', libraryId: 'music', itemKind: kind, actions: ['retry', 'dismiss'], title: 'Title', revision: 1, reason: 'unmatched', confidence: 0, candidates: [], artworkUrl}]};
}

test('attention covers are only the actual album or track-parent selected digest', () => {
  const url = `/v1/metadata/album/album/art/cover?v=${digest}`;
  for (const kind of ['album', 'song'] as const) {
    assert.equal(parseMetadataAttention(page(kind, url), 'music').entries[0]?.artworkUrl, url);
    for (const wrong of [url.replace('/album/art/', '/other/art/'), url + '&candidate=other', 'https://other.example' + url, url.replace(digest, 'not-a-digest')]) {
      assert.equal(parseMetadataAttention(page(kind, wrong), 'music').entries[0]?.artworkUrl, undefined);
    }
  }
});
