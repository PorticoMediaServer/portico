import test from 'node:test';
import assert from 'node:assert/strict';
import {withReadingDiscoverMarks} from '../src/reading-preview-mark.ts';

/** The canonical decoder stands in for the generated one: it refuses unknown properties. */
const decode = (value: unknown) => {
  const row = (value as {rows?: unknown[]}).rows?.[0] as {series?: {preview: Record<string, unknown>[]}[]} | undefined;
  for (const series of row?.series ?? []) for (const item of series.preview) if ('readingMark' in item) throw new Error('unknown property');
  return value as never;
};
const item = (id: string, mark?: unknown) => ({id, ...(mark === undefined ? {} : {readingMark: mark})});
const page = (preview: Record<string, unknown>[]) => ({rows: [{key: 'series', items: [], series: [{id: 'series', count: 3, readCount: 1, preview}]}]});

test('the optional mark is bound to its own item and never reaches the decoder', () => {
  const mark = {itemId: 'b', read: true, revision: 4};
  const {marks} = withReadingDiscoverMarks(page([item('a'), item('b', mark)]), decode);
  assert.deepEqual([...marks], [['b', mark]]);
});

test('a mark naming another item, or carrying no revision, is dropped', () => {
  const {marks} = withReadingDiscoverMarks(page([item('a', {itemId: 'b', read: true, revision: 4}), item('b', {itemId: 'b', read: true})]), decode);
  assert.equal(marks.size, 0);
});

test('two different marks for one item drop it rather than letting the last one win', () => {
  const {marks} = withReadingDiscoverMarks(page([item('a', {itemId: 'a', read: true, revision: 4}), item('a', {itemId: 'a', read: false, revision: 5})]), decode);
  assert.equal(marks.has('a'), false);
});

test('the same mark twice is one mark', () => {
  const mark = {itemId: 'a', read: false, revision: 2};
  const {marks} = withReadingDiscoverMarks(page([item('a', mark), item('a', mark)]), decode);
  assert.deepEqual(marks.get('a'), mark);
});
