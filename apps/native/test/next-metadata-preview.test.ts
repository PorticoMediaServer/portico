import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeBulkMedia} from '../src-next/screens/destinations/bulk-media.ts';
import {bulkFieldHint, bulkFieldStateLabel} from '../src-next/kit/selection/presentation.ts';
import type {CatalogueSelectionOptions} from '../../../packages/client-core/src/catalogue-selection.ts';
import {rebuiltMetadata} from '../../../packages/client-core/test/fixtures/rebuilt-metadata.ts';

const tick = async () => {for (let i = 0; i < 30; i++) await Promise.resolve();};
const entries = ['a', 'b'].map(id => ({id, libraryId: 'library', kind: 'movie' as const, title: id}));
const many = {state: 'many' as const, atLeast: 65, values: [], entries: [{value: 'Preserved', count: 17}], locked: 23, truncated: true};
const fake = () => rebuiltMetadata({a: {fields: {year: 1999}, locked: ['year']}, b: {fields: {year: 2001}}});
function fixture(request: CatalogueSelectionOptions['api']['request']) {
  const life = new AbortController();
  const bulk = new NativeBulkMedia({api: {request}, scope: {serverId: 'server', viewerId: 'owner'}, requestId: async () => 'request-id', signal: life.signal, isCurrent: () => !life.signal.aborted} as CatalogueSelectionOptions, entries);
  return {bulk, life};
}

test('native many presentation preserves the lower bound without formatting it as an exact count', () => {
  assert.equal(bulkFieldStateLabel(many), 'Many values');
  assert.equal(bulkFieldHint(many, 70, false), ''); assert.equal(bulkFieldHint(many, 70, true), 'was Mixed');
  assert.equal(bulkFieldHint({state: 'mixed', distinctCount: 2, values: [], entries: [], truncated: false}, 70, false), '2 values');
  assert.equal(bulkFieldHint({state: 'shared', distinctCount: 1, value: '', values: [], entries: [], truncated: false}, 70, false), 'All 70');
});
test('native editor reads chosen titles and shows Mixed, locks and an unchanged draft', async () => {
  const docs = fake();
  const f = fixture(async <T>(path: string, method?: string, body?: unknown): Promise<T> => docs.answer(path, method, body) as T);
  try {
    await f.bulk.openEditor(); const snapshot = f.bulk.getSnapshot();
    assert.equal(snapshot.phase, 'ready'); assert.equal(snapshot.values.year.state, 'mixed');
    assert.equal(snapshot.preview!.fields.year!.locked, 1);
    assert.deepEqual(snapshot.changes, {}); assert.equal(snapshot.lockEdited, true);
    assert.deepEqual(docs.reads, ['a', 'b']); assert.deepEqual(docs.jobs, []);
  } finally {f.bulk.dispose();}
});
test('closing a pending preview cannot revive the editor or send a write', async () => {
  const docs = fake(); const pending: (() => void)[] = [];
  const f = fixture(async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    const result = docs.answer(path, method, body); await new Promise<void>(resolve => pending.push(resolve)); return result as T;
  });
  try {
    const work = f.bulk.openEditor(); await tick();
    assert.equal(f.bulk.getSnapshot().phase, 'loading'); assert.equal(f.bulk.getSnapshot().preview, undefined);
    f.bulk.close(); pending.splice(0).forEach(finish => finish()); await work;
    assert.equal(f.bulk.getSnapshot().phase, 'idle'); assert.equal(f.bulk.getSnapshot().preview, undefined);
    assert.deepEqual(docs.jobs, []);
  } finally {f.bulk.dispose();}
});
