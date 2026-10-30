import {wireFixture} from './wire-fixture.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {parsePlayHistory as read_parsePlayHistory, parsePlaySummary as read_parsePlaySummary} from '../src/play-history.ts';

const interval={period:'today',timeZone:'UTC',from:'2026-10-02T00:00:00Z',until:'2026-10-02T12:00:00Z',buckets:[{start:'2026-10-02T00:00:00Z',end:'2026-10-02T12:00:00Z',partial:true}]};
const parsePlaySummary=(value:unknown,...args:any[]) => (read_parsePlaySummary as any)({...interval,...wireFixture(value,{'watchedSeconds':'watchedMs'},[]) as object},...args);
const parsePlayHistory=(value:unknown,...args:any[]) => (read_parsePlayHistory as any)({...interval,...wireFixture(value,{'positionSeconds':'positionMs','durationSeconds':'durationMs'},[]) as object},...args);

const play = (id = 'play-1', patch: Record<string, unknown> = {}) => ({id, user: 'justin', accountId: 'account', kind: 'movie', title: 'Movie', libraryId: 'library', startedAt: '2026-10-02T09:08:00Z', positionSeconds: 30, durationSeconds: 60, completed: false, ...patch});

test('one play this app cannot read is left out and the rest of the page is kept', () => {
  const page = parsePlayHistory({entries: [play('one'), play('bad', {startedAt: 'yesterday'}), 'nonsense', play('one', {title: 'Again'}), play('two', {futureField: {a: 1}})], total: 5, nextCursor: 'next', futureField: true});
  assert.deepEqual(page.entries.map(entry => [entry.id, entry.title]), [['one', 'Movie'], ['two', 'Movie']]);
  assert.equal(page.total, 5);
  assert.equal(page.nextCursor, 'next');
  assert.equal('futureField' in page.entries[1]!, false);
});

test('an optional detail that cannot be read is left off its play, and a kind this app does not know is kept as sent', () => {
  const page = parsePlayHistory({entries: [play('one', {kind: 'hologram', device: 7, season: -1, episode: 3, platform: 'web'})], total: 'many', nextCursor: ''});
  assert.deepEqual(page.entries[0], {...play('one', {kind: 'hologram'}), episode: 3, platform: 'web'});
  assert.equal(page.total, undefined);
});

test('the size of the page, its cursor and its outer shape still refuse the whole page', () => {
  assert.throws(() => parsePlayHistory({entries: Array.from({length: 201}, (_, i) => play('p' + i)), nextCursor: ''}));
  assert.throws(() => parsePlayHistory({entries: [play()], nextCursor: 7}));
  assert.throws(() => parsePlayHistory({entries: [play()], nextCursor: 'x'.repeat(257)}));
  assert.throws(() => parsePlayHistory({entries: {}, nextCursor: ''}));
  assert.throws(() => parsePlayHistory([]));
});

test('viewing statistics keep their totals when one named count cannot be read', () => {
  const summary = parsePlaySummary({plays: 4, people: 2, watchedSeconds: 7200, converted: 1, mostPlayed: [{name: 'Movie', plays: 3}, {name: 'Broken', plays: -1}, null], mostActive: [{name: 'justin', plays: 4}], futureField: 1});
  assert.deepEqual(summary, {...interval,plays: 4, people: 2, watchedMs: 7200000, converted: 1, mostPlayed: [{name: 'Movie', plays: 3}], mostActive: [{name: 'justin', plays: 4}]});
  assert.throws(() => parsePlaySummary({plays: 4, people: 2, watchedSeconds: 7200, converted: 1, mostPlayed: Array.from({length: 51}, () => ({name: 'Movie', plays: 1})), mostActive: []}));
  assert.throws(() => parsePlaySummary({plays: -1, people: 2, watchedSeconds: 7200, converted: 1, mostPlayed: [], mostActive: []}));
});
