import test from 'node:test';
import assert from 'node:assert/strict';
import {homeDocument} from '../src/home.ts';
import {homeEntryCapabilities} from '../src/home-selection.ts';
import {entryActions} from '../src/presentation/entry-actions.ts';

// TV-web walk (16 Nov): on the rebuilt server, which sends no per-card grants, a Home card's options offered only Select.
const state = (extra = {}) => ({watched: false, playCount: 0, favorite: false, inList: false, notInterested: false, ...extra});
const item = (id: string, kind: string, extra = {}) => ({id, kind, title: id, libraryId: 'L', addedAt: '', images: {}, state: state(), ...extra});
const ids = (entry: never) => entryActions(homeEntryCapabilities(entry, {platform: 'web', playbackAvailable: true, queueAvailable: true})).flatMap(group => group.actions.map(action => action.id));

test('a Home card offers what a title\'s page offers, and Continue Watching its removal', () => {
  const home = homeDocument({rows: [
    {id: 'continue', title: 'Continue Watching', shape: 'landscape', items: [item('ep', 'episode', {state: state({positionMs: 60000})})]},
    {id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [item('film', 'movie'), item('show', 'show', {counts: {children: 2, leaves: 10, unwatchedLeaves: 10}})]},
  ]} as never);
  const [resume, film, show] = [home.rows[0]!.entries[0], home.rows[1]!.entries[0], home.rows[1]!.entries[1]] as never[];
  const r = ids(resume), f = ids(film), s = ids(show);
  for (const id of ['resume', 'playFromStart', 'watchlist', 'favorite', 'watched', 'removeFromContinueWatching']) assert.ok(r.includes(id as never), `continue: ${id} in ${r}`);
  for (const id of ['play', 'watchlist', 'favorite', 'watched', 'rate']) assert.ok(f.includes(id as never), `film: ${id} in ${f}`);
  assert.ok(!f.includes('removeFromContinueWatching' as never), 'only a Continue row offers its removal');
  for (const id of ['watchlist', 'favorite', 'watched']) assert.ok(s.includes(id as never), `show: ${id} in ${s}`);
  for (const id of ['editMetadata', 'delete', 'refreshMetadata']) assert.ok(!f.includes(id as never), 'owner actions stay on the title\'s page');
});

test('Home See all opens the saved view or collection that owns the shelf', () => {
  const home = homeDocument({rows: [
    {id: 'view:lst_view', title: 'Rainy day films', shape: 'poster', items: [], library: 'L', query: 'kind=movie&genre=Drama', deferred: true},
    {id: 'collection:itm_collection', title: 'Series', shape: 'poster', items: [item('film', 'movie')], library: 'L', query: 'collection=itm_collection'},
  ]} as never);
  assert.deepEqual(home.rows.map(row => row.seeAll), [
    {type: 'saved_view', resourceId: 'lst_view'},
    {type: 'collection', resourceId: 'itm_collection', libraryId: 'L'},
  ]);
});
