import {wireFixture} from './wire-fixture.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {parsePlaySummary as read_parsePlaySummary} from '../src/play-history.ts';

const interval={period:'today',timeZone:'UTC',from:'2026-10-02T00:00:00Z',until:'2026-10-02T12:00:00Z',buckets:[{start:'2026-10-02T00:00:00Z',end:'2026-10-02T12:00:00Z',partial:true}]};
const parsePlaySummary=(value:unknown,...args:any[]) => (read_parsePlaySummary as any)({...interval,...wireFixture(value,{'watchedSeconds':'watchedMs'},[]) as object},...args);

test('viewing statistics keep their totals when one named count cannot be read', () => {
  const summary = parsePlaySummary({plays: 4, people: 2, watchedSeconds: 7200, converted: 1, mostPlayed: [{name: 'Movie', plays: 3}, {name: 'Broken', plays: -1}, null], mostActive: [{name: 'justin', plays: 4}], futureField: 1});
  assert.deepEqual(summary, {...interval,plays: 4, people: 2, watchedMs: 7200000, converted: 1, mostPlayed: [{name: 'Movie', plays: 3}], mostActive: [{name: 'justin', plays: 4}]});
  assert.throws(() => parsePlaySummary({plays: 4, people: 2, watchedSeconds: 7200, converted: 1, mostPlayed: Array.from({length: 51}, () => ({name: 'Movie', plays: 1})), mostActive: []}));
  assert.throws(() => parsePlaySummary({plays: -1, people: 2, watchedSeconds: 7200, converted: 1, mostPlayed: [], mostActive: []}));
});
