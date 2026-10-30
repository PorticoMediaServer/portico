import test from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeDeleteReadingSavedGroupReferenceResponse,
  decodeGetAdminAccessInvitationResponse,
  decodeGetBrowseFacetsResponse,
  decodeGetDownloadArtworkResponse,
  decodeGetItemsItemIdReadingMarkResponse,
  decodePostItemsIdAnalysisJobsJobControlResponse,
} from '../src/decoders.ts';

// The pre-fix oracle: the exact code-point length the generated decoder used to
// allocate a throwing-away array to compute. Captured before any spy is installed.
const realArrayFrom = Array.from;
const oracleLength = (text: string): number => (realArrayFrom as unknown as (value: string) => string[])(text).length;

type Counts = {arrays: number; codeUnits: number; patterns: number};
type Measured<T> = {counts: Counts; value?: T; error?: unknown};

// Counts every mechanism the generated decoder could use to measure a string, every
// pattern construction, and every Array.from allocation, for one decode call.
function measure<T>(run: () => T): Measured<T> {
  const counts: Counts = {arrays: 0, codeUnits: 0, patterns: 0};
  const patchedArray = Array as unknown as {from: (...args: unknown[]) => unknown};
  const realFrom = patchedArray.from;
  const realCharCodeAt = String.prototype.charCodeAt;
  const RealRegExp = globalThis.RegExp;
  patchedArray.from = (...args: unknown[]) => { counts.arrays += 1; return (realFrom as (...rest: unknown[]) => unknown)(...args); };
  String.prototype.charCodeAt = function (this: string, index: number): number { counts.codeUnits += 1; return realCharCodeAt.call(this, index); };
  globalThis.RegExp = (class extends RealRegExp {
    constructor(pattern: string | RegExp, flags?: string) { counts.patterns += 1; super(pattern, flags); }
  }) as RegExpConstructor;
  try {
    return {counts, value: run()};
  } catch (error) {
    return {counts, error};
  } finally {
    patchedArray.from = realFrom;
    String.prototype.charCodeAt = realCharCodeAt;
    globalThis.RegExp = RealRegExp;
  }
}

const savedGroupReference = (serverId: string) => ({serverId, referenceId: 'ref_1', revision: 1, removed: true, operationId: 'op_1'});

// GetAdminAccessInvitationResponse.notes is the only bounded string (maxLength 500);
// every other string in that DTO is unbounded.
const invitation = (notes?: string | null) => ({
  id: 'inv_1', role: 'member', allowedLibraries: ['lib_1'], state: 'pending',
  createdAt: '2026-01-01T00:00:00Z', expiresAt: '2026-01-02T00:00:00Z', revision: 1,
  access: {accessTokens: false, allowRemote: true, maxContentRating: '', maxStreams: 0, maxStreamsMode: 'inherit'},
  ...(notes === undefined ? {} : {notes}),
});

const SAMPLE_ERROR = {message: 'Invalid contract value at $.serverId'};
const NOTES_ERROR = {message: 'Invalid contract value at $.notes'};

test('generated bounded-string decoder accepts exactly the Array.from code-point oracle for every Unicode class', () => {
  const samples = [
    '', 'a', 'a'.repeat(128), 'a'.repeat(129),
    '😀', '😀'.repeat(64), '😀'.repeat(64) + 'a', '😀'.repeat(65),
    'e\u0301', 'e\u0301'.repeat(64), 'e\u0301'.repeat(64) + 'e',
    '\uD800', '\uDC00', '\uD83D\uDE00', '\uD800\uD800', '\uDC00\uDC00',
    'a\uD800', '\uD83D\uDE00\uD800', '\uD800\uD83D\uDE00',
  ];
  for (const sample of samples) {
    const expected = oracleLength(sample);
    const accepts = expected >= 1 && expected <= 128;
    const attempt = () => decodeDeleteReadingSavedGroupReferenceResponse(savedGroupReference(sample));
    if (accepts) assert.doesNotThrow(attempt, `Array.from length ${expected} is inside 1..128`);
    else assert.throws(attempt, SAMPLE_ERROR, `Array.from length ${expected} is outside 1..128`);
  }
  // The oracle is only discriminating because it disagrees with UTF-16 .length.
  assert.equal('😀'.repeat(64).length, 128);
  assert.equal('e\u0301'.repeat(64).length, 128);
});

test('generated maxLength counts code points, not UTF-16 units, for astral and combining sequences', () => {
  const samples = [
    'a'.repeat(500), 'a'.repeat(501),
    '😀'.repeat(500), '😀'.repeat(501),
    'e\u0301'.repeat(250), 'e\u0301'.repeat(250) + 'e',
  ];
  for (const sample of samples) {
    const expected = oracleLength(sample);
    const attempt = () => decodeGetAdminAccessInvitationResponse(invitation(sample));
    if (expected <= 500) assert.doesNotThrow(attempt, `Array.from length ${expected} is inside 0..500`);
    else assert.throws(attempt, NOTES_ERROR, `Array.from length ${expected} exceeds 500`);
  }
  assert.equal('😀'.repeat(500).length, 1000, 'astral sample has twice as many UTF-16 units as code points');
  assert.equal('e\u0301'.repeat(250).length, 500, 'combining sample has one code point per pair');
});

test('every lone surrogate is one code point and an unpaired high surrogate never pairs with the next one', () => {
  const high = '\uD800'.repeat(500);
  const low = '\uDC00'.repeat(500);
  const pairs = '\uD83D\uDE00'.repeat(500);
  const mixed = '\uD800\uD83D\uDE00'.repeat(250);
  for (const sample of [high, low, pairs, mixed]) {
    assert.equal(oracleLength(sample), 500);
    assert.doesNotThrow(() => decodeGetAdminAccessInvitationResponse(invitation(sample)));
  }
  assert.equal(high.length + low.length + pairs.length + mixed.length, 500 + 500 + 1000 + 750);
  assert.throws(() => decodeGetAdminAccessInvitationResponse(invitation('\uD800'.repeat(501))), NOTES_ERROR);
  assert.throws(() => decodeGetAdminAccessInvitationResponse(invitation('\uD83D\uDE00'.repeat(501))), NOTES_ERROR);
  assert.throws(() => decodeGetAdminAccessInvitationResponse(invitation('\uD800\uD83D\uDE00'.repeat(250) + '\uDC00')), NOTES_ERROR);
});

test('a pattern-only field is validated without allocating a code-point array or walking the string', () => {
  const facets = (seriesId: string) => ({field: 'genre', revision: 'rev_1', total: 1, totalIsLowerBound: false, values: [{value: 'value', label: 'Label', count: 1, countIsLowerBound: false, seriesId}]});
  const accepted = measure(() => decodeGetBrowseFacetsResponse(facets('A'.repeat(43))));
  assert.equal(accepted.error, undefined);
  assert.deepEqual(accepted.counts, {arrays: 0, codeUnits: 0, patterns: 1});
  for (const bad of ['A'.repeat(42), 'A'.repeat(44), 'A'.repeat(42) + '!']) {
    assert.throws(() => decodeGetBrowseFacetsResponse(facets(bad)), {message: 'Invalid contract value at $.values[0].seriesId'});
  }
});

test('a document of only unbounded strings allocates no code-point array and walks no string', () => {
  const unbounded = measure(() => decodeGetAdminAccessInvitationResponse(invitation()));
  assert.equal(unbounded.error, undefined);
  assert.deepEqual(unbounded.counts, {arrays: 0, codeUnits: 0, patterns: 0});
  const bounded = measure(() => decodeGetAdminAccessInvitationResponse(invitation('😀'.repeat(10))));
  assert.equal(bounded.error, undefined);
  assert.equal(bounded.counts.arrays, 0);
  assert.equal(bounded.counts.codeUnits, 20, 'one charCodeAt per UTF-16 unit of the bounded notes value');
});

test('a multi-megabyte unbounded string is not walked at all, while the same instrumentation observes a bounded walk', () => {
  const payload = 'x'.repeat(2 * 1024 * 1024);
  const binary = measure(() => decodeGetDownloadArtworkResponse(payload));
  assert.equal(binary.error, undefined);
  assert.deepEqual(binary.counts, {arrays: 0, codeUnits: 0, patterns: 0});
  const control = measure(() => decodeGetAdminAccessInvitationResponse(invitation('abc')));
  assert.equal(control.error, undefined);
  assert.deepEqual(control.counts, {arrays: 0, codeUnits: 3, patterns: 0});
});

test('length failures reject before any pattern is constructed, and pattern failures keep the same message', () => {
  const mark = (itemId: string) => ({serverId: 'srv_1', viewerFence: 'fence_1', itemId, read: true, revision: 0});
  for (const early of ['', 'A'.repeat(65)]) {
    const measured = measure(() => decodeGetItemsItemIdReadingMarkResponse(mark(early)));
    assert.deepEqual(measured.error, new Error('Invalid contract value at $.itemId'));
    assert.equal(measured.counts.patterns, 0, 'no RegExp is constructed when the length bound already fails');
  }
  for (const late of ['A'.repeat(64), '!'.repeat(10)]) {
    const measured = measure(() => decodeGetItemsItemIdReadingMarkResponse(mark(late)));
    assert.equal(measured.counts.patterns, 1);
    if (late.startsWith('A')) assert.equal(measured.error, undefined);
    else assert.deepEqual(measured.error, new Error('Invalid contract value at $.itemId'));
  }
});

test('the generated DTOs still sanitise, keep null prototypes, drop optional nulls and additive fields, and reject closed or unsupported values', () => {
  const source = {...invitation('notes'), futureField: 'extension', notes: null};
  const result = decodeGetAdminAccessInvitationResponse(source) as Record<string, unknown>;
  assert.equal(Object.getPrototypeOf(result), null);
  assert.equal('futureField' in result, false);
  assert.equal('notes' in result, false, 'an optional null is absent');
  assert.equal(result.state, 'pending');
  assert.notEqual(result.allowedLibraries, source.allowedLibraries, 'arrays are rebuilt, not returned from the parsed input');
  assert.throws(() => decodeGetAdminAccessInvitationResponse({...invitation(), id: undefined}), {message: 'Invalid contract value at $.id'});
  assert.throws(() => decodeDeleteReadingSavedGroupReferenceResponse({...savedGroupReference('srv_1'), extra: 1}), {message: 'Invalid contract value at $'});
  assert.throws(() => decodeDeleteReadingSavedGroupReferenceResponse({...savedGroupReference('srv_1'), removed: false}), {message: 'Invalid contract value at $.removed'});
  const control = {id: 'job_1', status: 'running', processed: 1, analyzed: 2, warnings: 0};
  const anyOf = measure(() => decodePostItemsIdAnalysisJobsJobControlResponse(control));
  assert.equal(anyOf.error, undefined);
  assert.deepEqual(anyOf.counts, {arrays: 0, codeUnits: 0, patterns: 0});
  assert.throws(() => decodePostItemsIdAnalysisJobsJobControlResponse({id: 'job_1'}), {message: 'Invalid contract value at $'});
});
