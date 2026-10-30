import assert from 'node:assert/strict';
import test from 'node:test';
import type {GetReadingSessionsSessionIdSearchResponse} from '../../contracts/src/index.ts';
import {ReadingClient, ReadingSession, parseReadingSearchPage, type ReadingManifest, type ReadingSearchWitness} from '../src/reading.ts';

type Raw = GetReadingSessionsSessionIdSearchResponse;

const witness: ReadingSearchWitness = {
  sessionId: 'session', sourceId: 'asset', expectedRevision: 'a'.repeat(64),
  format: 'epub', converted: false, spine: ['OEBPS/chapter-1.xhtml'],
  xhtml: path => path === 'OEBPS/chapter-1.xhtml',
};
const hit = {sectionPath: 'OEBPS/chapter-1.xhtml', sectionIndex: 0, utf16Offset: 40, excerpt: 'The quick brown fox.', matchStart: 4, matchLength: 5};
function page(overrides: Partial<Raw> = {}): Raw {
  return {sessionId: witness.sessionId, sourceId: witness.sourceId, sourceRevision: witness.expectedRevision, indexRevision: 3,
    coverage: {status: 'indexed'}, hits: [hit], ...overrides} as Raw;
}

test('accepts an indexed page and distinguishes section offset from excerpt range', () => {
  const parsed = parseReadingSearchPage(page(), witness, 20);
  assert.equal(parsed.hits.length, 1);
  assert.equal(parsed.hits[0]!.utf16Offset, 40);
  assert.equal(parsed.hits[0]!.matchStart, 4);
  assert.equal(parsed.hits[0]!.matchLength, 5);
  assert.equal(parsed.indexRevision, 3);
  assert.equal(parsed.next, undefined);
  assert.equal(Object.isFrozen(parsed), true);
  assert.equal(Object.isFrozen(parsed.hits), true);
  assert.equal(Object.isFrozen(parsed.hits[0]), true);
  assert.equal(Object.isFrozen(parsed.coverage), true);
});

test('rejects a response that is not the retained original witness', () => {
  for (const overrides of [{sessionId: 'other'}, {sourceId: 'other'}, {sourceRevision: 'b'.repeat(64)}]) {
    assert.throws(() => parseReadingSearchPage(page(overrides), witness, 20), /invalid_document/);
  }
});

test('converted and non-EPUB sessions only accept the explicit unsupported fallback', () => {
  const converted = {...witness, converted: true, expectedRevision: 'c'.repeat(64)};
  assert.throws(() => parseReadingSearchPage({...page(), sourceRevision: converted.expectedRevision}, converted, 20), /invalid_document/);
  const fallback = parseReadingSearchPage({...page(), sourceRevision: converted.expectedRevision, coverage: {status: 'unsupported', reason: 'format'}, hits: []}, converted, 20);
  assert.equal(fallback.coverage.status, 'unsupported');
  assert.equal(fallback.hits.length, 0);
  const pdf = {...witness, format: 'pdf'};
  assert.throws(() => parseReadingSearchPage(page(), pdf, 20), /invalid_document/);
});

test('an indexed hit must be the exact manifest spine XHTML member', () => {
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, sectionIndex: 1}]}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, sectionPath: 'OEBPS/other.xhtml'}]}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page(), {...witness, xhtml: () => false}, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, sectionPath: '../escape.xhtml'}]}), witness, 20), /invalid_document/);
});

test('nonindexed coverage never carries hits or a continuation but is returned as fallback evidence', () => {
  const fallback = parseReadingSearchPage(page({coverage: {status: 'pending'}, hits: []}), witness, 20);
  assert.equal(fallback.coverage.status, 'pending');
  assert.equal(fallback.hits.length, 0);
  assert.equal(fallback.next, undefined);
  assert.throws(() => parseReadingSearchPage(page({coverage: {status: 'pending'}}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({coverage: {status: 'evicted'}, hits: [], next: 'abc'}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({coverage: {status: 'unknown'}, hits: []}), witness, 20), /invalid_document/);
});

test('rejects duplicate section positions even when the excerpt range differs', () => {
  assert.throws(() => parseReadingSearchPage(page({hits: [hit, hit]}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [hit, {...hit, excerpt: 'Another excerpt.', matchStart: 0, matchLength: 7}]}), witness, 20), /invalid_document/);
});

test('rejects duplicate, oversized and malformed hits', () => {
  assert.throws(() => parseReadingSearchPage(page({hits: [hit]}), witness, 0), /invalid_request/);
  const many = Array.from({length: 51}, (_, index) => ({...hit, utf16Offset: index}));
  assert.throws(() => parseReadingSearchPage(page({hits: many}), witness, 50), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, matchStart: 18}]}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, matchLength: 0}]}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, utf16Offset: -1}]}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, sectionIndex: 4096}]}), witness, 20), /invalid_document/);
});

test('rejects a highlight that splits a surrogate pair or an unpaired excerpt', () => {
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, excerpt: '😀x', matchStart: 1, matchLength: 1}]}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({hits: [{...hit, excerpt: '\ud800x'}]}), witness, 20), /invalid_document/);
  const pair = parseReadingSearchPage(page({hits: [{...hit, excerpt: '😀x', matchStart: 0, matchLength: 2}]}), witness, 20);
  assert.equal(pair.hits[0]!.matchLength, 2);
});

test('bounds the opaque cursor and preserves a valid continuation', () => {
  const next = 'A'.repeat(512);
  assert.equal(parseReadingSearchPage(page({next}), witness, 20).next, next);
  assert.throws(() => parseReadingSearchPage(page({next: 'A'.repeat(513)}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({next: 'not/base64url'}), witness, 20), /invalid_document/);
  assert.throws(() => parseReadingSearchPage(page({next: ''}), witness, 20), /invalid_document/);
});

test('the public parser decodes unknown input itself', () => {
  assert.throws(() => parseReadingSearchPage(null, witness, 20), /invalid_document/);
  // R-09b: responses read open. A newer server's added members are ignored, never refused.
  assert.doesNotThrow(() => parseReadingSearchPage({...page(), extra: 1}, witness, 20));
  assert.doesNotThrow(() => parseReadingSearchPage({...page(), hits: [{...hit, extra: 1}]}, witness, 20));
  assert.throws(() => parseReadingSearchPage('not an object', witness, 20), /invalid_document/);
});

test('rejects an invalid page limit even with zero hits', () => {
  const pending = page({coverage: {status: 'pending'}, hits: []});
  assert.throws(() => parseReadingSearchPage(pending, witness, 0), /invalid_request/);
  assert.throws(() => parseReadingSearchPage(pending, witness, 51), /invalid_request/);
});

test('rejects a malformed query before any request is sent', async () => {
  let sent = 0;
  const capture = {send: async () => { sent++; throw new Error('not reached'); }, isCurrent: () => true, subscribe: () => () => {}};
  const client = new ReadingClient(capture as never, {viewer: {serverId: 'server'} as never, isCurrent: () => true});
  const manifest = {sessionId: 'session', itemId: 'item', sourceId: 'asset', revision: 'a'.repeat(64), format: 'epub', size: 1,
    metadata: {title: 'Title'}, spine: ['OEBPS/chapter-1.xhtml'], resources: [{path: 'OEBPS/chapter-1.xhtml', mime: 'application/xhtml+xml', size: 1}]} as unknown as ReadingManifest;
  const session = new ReadingSession(client, manifest);
  await assert.rejects(session.search('\ud800'), (error: {code?: string}) => error.code === 'invalid_request');
  await assert.rejects(session.search('ok', {limit: 0}), (error: {code?: string}) => error.code === 'invalid_request');
  await assert.rejects(session.search('ok', {cursor: 'not/base64url'}), (error: {code?: string}) => error.code === 'invalid_request');
  assert.equal(sent, 0);
});

test('rejects a coverage reason that is not bounded text', () => {
  assert.throws(() => parseReadingSearchPage(page({coverage: {status: 'unsupported', reason: 'x'.repeat(65)}, hits: []}), witness, 20), /invalid_document/);
  const parsed = parseReadingSearchPage(page({coverage: {status: 'unsupported', reason: 'version'}, hits: []}), witness, 20);
  assert.equal(parsed.coverage.reason, 'version');
});
