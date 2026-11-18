import test from 'node:test';
import assert from 'node:assert/strict';
import {ReadingAnnotationService} from '../src/reading-annotation-service.ts';
import type {ReadingAnnotationContent, ReadingAnnotationMutation} from '../src/reading-annotations.ts';

async function withoutStructuredClone(run: () => Promise<void>) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'structuredClone');
  delete (globalThis as {structuredClone?: unknown}).structuredClone;
  try { await run(); }
  finally { if (descriptor) Object.defineProperty(globalThis, 'structuredClone', descriptor); }
}

test('annotation write captures its actual mutation and settles without global structuredClone', () => withoutStructuredClone(async () => {
  const life = new AbortController();
  const draft: ReadingAnnotationContent = {id: 'mark', kind: 'bookmark', location: {format: 'pdf', pageIndex: 2, progression: .2}, text: '', note: 'Original note', colour: ''};
  let revision = 1, release!: () => void;
  const held = new Promise<void>(resolve => {release = resolve;});
  const sent: ReadingAnnotationMutation[] = [];
  const service = new ReadingAnnotationService({
    manifest: {sourceId: 'source', revision: 'source-r1', canAnnotate: true}, signal: life.signal,
    annotations: async () => ({sourceId: 'source', sourceRevision: 'source-r1', revision, entries: [], nextCursor: ''}),
    mutateAnnotation: async mutation => {sent.push(mutation); await held; revision++; return {revision};},
  }, () => true);
  try {
    await service.load();
    const write = service.write(draft, {revision: 1, signal: life.signal, isCurrent: () => true});
    assert.equal(sent.length, 1, 'the actual mutation port is reached');
    assert.equal(sent[0].expectedRevision, 1); assert.equal(sent[0].operation, 'upsert');
    assert('location' in sent[0]); assert.notEqual(sent[0].location, draft.location);
    Object.assign(draft.location, {pageIndex: 9}); Object.assign(draft, {note: 'Changed after dispatch'});
    assert.equal(sent[0].location.pageIndex, 2); assert.equal(sent[0].note, 'Original note');
    release(); assert.equal(await write, true); assert.equal(service.getSnapshot().revision, 2);
    assert.equal(service.getSnapshot().saving, false); assert.equal(service.getSnapshot().error, undefined);
  } finally {release(); service.dispose(); life.abort();}
}));
