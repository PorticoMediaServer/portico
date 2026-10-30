import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';
import {schemas} from '../src/schemas.ts';

const journey = {from: 'song-from', to: 'song-to', length: 12};
const read = (selector: unknown) => decode('CreateQueueRequest', {selector});
const forms = [
  {container: {kind: 'album', id: 'album'}},
  {query: {libraryId: 'library', pivot: 'songs'}},
  {saved: {id: 'saved-view'}},
  {items: {ids: ['song']}},
  {journey},
];

test('queue selectors accept exactly one of all four existing forms or a journey', () => {
  for (const selector of forms) assert.doesNotThrow(() => read(selector));
  for (const invalid of [{}, {other: {}}, ...forms.map(selector => ({...selector, other: true}))]) {
    assert.throws(() => read(invalid));
  }
  for (let first = 0; first < forms.length; first++) {
    for (let second = first + 1; second < forms.length; second++) {
      assert.throws(() => read({...forms[first], ...forms[second]}));
    }
  }
  assert.throws(() => read({journey, items: null}));
  assert.throws(() => read({journey: null}));
  assert.throws(() => decode('CreateQueueRequest', {segments: [{source: null}]}));
});

test('journey wire facts require both bounded endpoint IDs and a whole length from 2 to 128', () => {
  for (const length of [2, 12, 128]) assert.doesNotThrow(() => read({journey: {...journey, length}}));
  for (const length of [undefined, null, '12', true, 0, 1, 129, 2.5, NaN, Infinity]) {
    assert.throws(() => read({journey: {...journey, length}}));
  }
  for (const field of ['from', 'to']) {
    for (const value of [undefined, null, 7, '', 'bad id', '/private/path', 'x'.repeat(129)]) {
      assert.throws(() => read({journey: {...journey, [field]: value}}));
    }
  }
  for (const field of ['model', 'version', 'seed', 'shuffle', 'ordinals', 'keys']) {
    assert.throws(() => read({journey: {...journey, [field]: 'client-supplied'}}));
  }
});

test('create and segment request schemas share the closed selector union without a smoother flag', () => {
  const selectors = [
    schemas.CreateQueueRequest.properties.selector,
    schemas.CreateQueueRequest.properties.segments.items.properties.source,
    schemas.AddQueueSegmentRequest.properties.source,
    schemas.BatchQueueRequest.properties.source,
  ];
  for (const selector of selectors) {
    assert.deepEqual(selector.oneOf.map(variant => variant.required[0]).sort(), ['container', 'items', 'journey', 'query', 'saved']);
    for (const variant of selector.oneOf) assert.equal(variant.additionalProperties, false);
    const branch = selector.oneOf.find(variant => 'journey' in variant.properties)!;
    const shape = branch.properties.journey;
    assert.equal(shape.additionalProperties, false);
    assert.deepEqual([...shape.required].sort(), ['from', 'length', 'to']);
    assert.equal(shape.properties.length.type, 'integer');
    assert.equal(shape.properties.length.minimum, 2);
    assert.equal(shape.properties.length.maximum, 128);
  }
});
