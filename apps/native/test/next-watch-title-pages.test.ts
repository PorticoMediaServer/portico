import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeTitlePages, type NativeTitlePage} from '../src-next/screens/watch/title-pages.ts';

const episode = (id: string) => ({id, kind: 'episode' as const, title: id});
const season = (id: string) => ({id, showId: 'show', number: 1, title: id});
const first: NativeTitlePage = {key: 'season1/all', fence: 'scope/rev1', episodeCursor: null, seasonCursor: null, episodes: [episode('e1'), episode('e2')], seasons: [season('s1')]};

test('real episode/season pages append without changing already-read physical item order', () => {
  const pages = new NativeTitlePages(); pages.ingest(first);
  pages.ingest({...first, episodeCursor: 'next', episodes: [episode('e2'), episode('e3')]});
  assert.deepEqual(pages.getSnapshot().episodes.map(entry => entry.id), ['e1', 'e2', 'e3']);
  pages.ingest({...first, episodeCursor: 'next', episodes: [episode('e2'), episode('e3')], seasonCursor: 'more', seasons: [season('s2')]});
  assert.deepEqual(pages.getSnapshot().seasons.map(entry => entry.id), ['s1', 's2']);
});

test('changing season/filter and returning retains only previously read pages under the exact fence', () => {
  const pages = new NativeTitlePages(); pages.ingest(first);
  pages.ingest({...first, episodeCursor: 'next', episodes: [episode('e3')]});
  pages.ingest({...first, key: 'season1/unplayed', episodes: [episode('e2')]});
  assert.deepEqual(pages.getSnapshot().episodes.map(entry => entry.id), ['e2']);
  pages.ingest(first); assert.deepEqual(pages.getSnapshot().episodes.map(entry => entry.id), ['e1', 'e2', 'e3']);
});

test('a changed viewer/catalog fence drops every cached query and disposal drops all rows', () => {
  const pages = new NativeTitlePages(); pages.ingest(first);
  pages.ingest({...first, episodeCursor: 'next', episodes: [episode('e3')]});
  pages.ingest({...first, fence: 'scope/rev2', episodes: [episode('e4')]});
  assert.deepEqual(pages.getSnapshot().episodes.map(entry => entry.id), ['e4']);
  pages.dispose(); assert.deepEqual(pages.getSnapshot(), {episodes: [], seasons: []});
});
