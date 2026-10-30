import test from 'node:test';
import assert from 'node:assert/strict';
import {referenceFields, referenceSingleItemFields} from '../src-next/dev/families/selection/reference-fields.ts';

test('reference bulk values retain complete scalar and list-vector distributions', () => {
  for (const kind of ['movie', 'episode', 'song'] as const) for (const value of Object.values(referenceFields(kind))) {
    assert.equal(value.truncated, false);
    assert.equal(value.distinctCount, value.values.length);
    assert.equal(value.values.reduce((sum, row) => sum + row.count, 0), kind === 'episode' ? 3 : 4);
    if (value.state === 'shared') {
      assert.equal(value.distinctCount, 1); assert.equal(value.values[0]?.value, value.value);
    } else {
      assert.equal(value.value, undefined); assert.ok(value.distinctCount > 1);
    }
  }
  const fields = referenceFields();
  assert.equal(fields.edition.value, '');
  assert.deepEqual(fields.labels.entries, []);
  assert.deepEqual(fields.genres.entries.map(row => row.count), [4, 4, 3, 3]);
  assert.equal(referenceFields('episode').contentRating, undefined, 'an unavailable reference observation cannot inherit a movie rating');
});

test('each reference editor supplies the actual excluded field registry for its kind', () => {
  assert.deepEqual(referenceSingleItemFields('movie').map(field => field.field), ['title', 'sortTitle', 'originalTitle', 'tagline', 'description', 'releaseDate']);
  assert.deepEqual(referenceSingleItemFields('episode').map(field => field.field), ['title', 'sortTitle', 'description', 'releaseDate', 'seasonNumber', 'episodeNumber']);
  assert.deepEqual(referenceSingleItemFields('song').map(field => field.field), ['title', 'sortTitle', 'description', 'trackNumber', 'discNumber']);
  for (const kind of ['movie', 'episode', 'song'] as const) for (const field of referenceSingleItemFields(kind)) {
    assert.equal(field.bulk, false); assert.ok(field.label.length);
  }
});
