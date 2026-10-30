import {test} from 'node:test';
import assert from 'node:assert/strict';
import {
  selectableSelectionKind, selectionActions, selectionCapabilities, selectionFourthAction,
  selectionKindHasBulkFields, summarizeSelection,
  type SelectionCapabilities, type SelectionEnvironment, type SelectionItem, type SelectionKind,
} from '../src/selection-model/index.ts';

const all: SelectionCapabilities = {
  play: true, queue: true, playlist: true, myList: true, watched: true, collection: true,
  favorite: true, download: true, edit: true, refresh: true, delete: true,
};
const owner: SelectionEnvironment = {platform: 'desktop-web', isOwner: true, canEditMetadata: true};
function item(kind: Exclude<SelectionKind, 'person' | 'channel'>, id: string, libraryId = 'library', offers: Partial<SelectionCapabilities> = all): SelectionItem {
  return {kind, id, libraryId, capabilities: selectionCapabilities(kind, offers)};
}

test('all kinds remain selectable on Search, including People, Artists, Audiobooks and Live TV', () => {
  for (const kind of ['movie', 'show', 'season', 'episode', 'artist', 'album', 'song', 'book', 'audiobook_file', 'chapter', 'collection', 'category', 'author', 'book_series', 'disc', 'extra', 'person', 'channel'] as const) {
    assert.equal(selectableSelectionKind(kind, 'search'), true, kind);
    assert.equal(selectableSelectionKind(kind, 'catalogue'), true, kind);
  }
  assert.equal(selectableSelectionKind('unknown' as SelectionKind, 'search'), false);
});

test('actual supplied offers are required and truthy unknown values never grant a command', () => {
  const missing = selectionCapabilities('movie', {});
  assert(Object.values(missing).every(value => value === false));
  assert.equal(selectionCapabilities('movie', {play: 'true', queue: 1, edit: {}} as unknown as Partial<SelectionCapabilities>).play, false);
  const rules = selectionActions(summarizeSelection([item('movie', 'a', 'library', {})]), owner);
  for (const name of ['play', 'play-next', 'play-later', 'playlist', 'my-list', 'mark-watched', 'download', 'refresh'] as const) {
    assert.equal(rules[name].state, 'hidden', name);
  }
  assert.notEqual(rules.edit.state, 'ready'); assert.notEqual(rules.delete.state, 'ready');
  assert(Object.values(selectionCapabilities('unknown' as SelectionKind, all)).every(value => value === false));
});

test('lawful mixed catalogue selections keep Play, queue, playlist and My List without a same-kind restriction', () => {
  const summary = summarizeSelection([item('movie', 'm', 'movies'), item('episode', 'e', 'tv'), item('album', 'a', 'music'), item('song', 's', 'music')]);
  const rules = selectionActions(summary, owner);
  for (const name of ['play', 'play-next', 'play-later', 'playlist', 'my-list'] as const) assert.equal(rules[name].state, 'ready', name);
  assert.equal(rules['mark-watched'].state, 'hidden');
  assert.equal(rules['mark-unwatched'].state, 'hidden');
  assert.deepEqual(rules.edit, {state: 'explain', reason: 'mixed-types'});
  assert.deepEqual(rules.delete, {state: 'disabled', reason: 'one-library-required'});
  assert.deepEqual(summary.types, [{kind: 'movie', count: 1}, {kind: 'episode', count: 1}, {kind: 'album', count: 1}, {kind: 'song', count: 1}]);
});

test('mixed movie/show/episode picks keep watched only when every actual item offers it', () => {
  const selections = [item('movie', 'm'), item('show', 'show'), item('episode', 'episode')];
  assert.equal(selectionActions(summarizeSelection(selections), owner)['mark-watched'].state, 'ready');
  assert.equal(selectionActions(summarizeSelection([...selections, item('episode', 'denied', 'library', {...all, watched: false})]), owner)['mark-watched'].state, 'hidden');
  assert.equal(selectionCapabilities('song', all).watched, false, 'Played is not the catalogue Watched action');
});

test('one denied offer hides that command for the entire selection without changing unrelated actions', () => {
  const summary = summarizeSelection([item('movie', 'yes'), item('movie', 'no', 'library', {...all, queue: false})]);
  const rules = selectionActions(summary, owner);
  assert.equal(rules.play.state, 'ready'); assert.equal(rules.playlist.state, 'ready');
  assert.equal(rules['play-next'].state, 'hidden'); assert.equal(rules['play-later'].state, 'hidden');
});

test('curation uses both supplied edit offers and the actual audience; hosted owners are not excluded', () => {
  const summary = summarizeSelection([item('movie', 'a'), item('movie', 'b')]);
  assert.equal(selectionActions(summary, owner).edit.state, 'ready');
  assert.equal(selectionActions(summary, {...owner, isOwner: false, canEditMetadata: true}).edit.state, 'ready');
  const member = selectionActions(summary, {...owner, isOwner: false, canEditMetadata: false});
  assert.equal(member.edit.state, 'hidden'); assert.equal(member.delete.state, 'hidden'); assert.equal(member.refresh.state, 'hidden');
  assert.deepEqual(selectionActions(summarizeSelection([item('movie', 'denied', 'library', {})]), owner).edit, {state: 'disabled', reason: 'not-permitted'});
  assert.equal(selectionActions(summarizeSelection([item('season', 'season')]), owner).edit.state, 'explain');
  assert.equal(selectionKindHasBulkFields('season'), false);
  assert.equal(selectionKindHasBulkFields('channel'), false);
  assert.equal(selectionKindHasBulkFields('person'), false);
});

test('every platform admits authorized catalogue curation and only the phone app offers Download', () => {
  const summary = summarizeSelection([item('movie', 'a')]);
  for (const platform of ['desktop-web', 'mobile-web', 'phone-app', 'tv'] as const) {
    const rules = selectionActions(summary, {...owner, platform});
    for (const action of ['edit', 'delete', 'refresh'] as const) assert.equal(rules[action].state, 'ready', `${platform} ${action}`);
    assert.equal(rules.download.state, platform === 'phone-app' ? 'ready' : 'hidden', platform);
  }
});

test('TV Edit needs both editing authority and every actual grant, including Mixed and no-fields explanations', () => {
  const tv = {...owner, platform: 'tv' as const};
  const movies = summarizeSelection([item('movie', 'a'), item('movie', 'b')]);
  const editor = {...tv, isOwner: false, canEditMetadata: true};
  const viewer = {...tv, isOwner: false, canEditMetadata: false};
  assert.equal(selectionActions(movies, editor).edit.state, 'ready');
  for (const action of ['edit', 'refresh', 'delete'] as const) assert.equal(selectionActions(movies, viewer)[action].state, 'hidden', action);
  assert.equal(selectionActions(movies, editor).refresh.state, 'hidden');
  assert.equal(selectionActions(movies, editor).delete.state, 'hidden');
  const denied = summarizeSelection([item('movie', 'a'), item('movie', 'b', 'library', {...all, edit: false})]);
  assert.deepEqual(selectionActions(denied, tv).edit, {state: 'disabled', reason: 'not-permitted'});
  assert.deepEqual(selectionActions(denied, editor).edit, {state: 'disabled', reason: 'not-permitted'});
  const mixed = summarizeSelection([item('movie', 'movie'), item('episode', 'episode')]);
  assert.deepEqual(selectionActions(mixed, editor).edit, {state: 'explain', reason: 'mixed-types'});
  assert.equal(selectionActions(mixed, viewer).edit.state, 'hidden');
  assert.deepEqual(selectionActions(summarizeSelection([item('season', 'season')]), editor).edit, {state: 'explain', reason: 'no-bulk-fields'});
  assert.deepEqual(selectionActions(summarizeSelection([item('season', 'denied', 'library', {...all, edit: false})]), editor).edit, {state: 'disabled', reason: 'not-permitted'});
  for (const kind of ['movie', 'show', 'episode', 'song', 'album', 'artist', 'book', 'audiobook_file'] as const) {
    assert.equal(selectionActions(summarizeSelection([item(kind, kind)]), editor).edit.state, 'ready', kind);
  }
});

test('TV owner Delete and Refresh preserve actual offers, one-library safety and the explanation limit', () => {
  const tv = {...owner, platform: 'tv' as const};
  const denied = summarizeSelection([item('movie', 'a'), item('movie', 'b', 'library', {...all, delete: false, refresh: false})]);
  assert.deepEqual(selectionActions(denied, tv).delete, {state: 'disabled', reason: 'not-permitted'});
  assert.equal(selectionActions(denied, tv).refresh.state, 'hidden');
  assert.equal(selectionActions(denied, tv).edit.state, 'ready');
  const one = summarizeSelection([item('movie', 'a')]);
  assert.deepEqual(selectionActions({...one, count: 201}, tv).delete, {state: 'explain', reason: 'delete-limit'});
  assert.deepEqual(selectionActions(summarizeSelection([item('movie', 'a', 'one'), item('movie', 'b', 'two')]), tv).delete, {state: 'disabled', reason: 'one-library-required'});
  assert(Object.values(selectionActions(summarizeSelection([]), tv)).every(rule => rule.state === 'hidden'));
});

test('Delete over 200 opens the limit explanation and never implies destructive chunking', () => {
  const one = summarizeSelection([item('movie', 'a')]);
  const summary = {...one, count: 50_000, types: [{kind: 'movie' as const, count: 50_000}]};
  assert.deepEqual(selectionActions(summary, owner).delete, {state: 'explain', reason: 'delete-limit'});
  assert.equal(selectionActions({...summary, count: 200}, owner).delete.state, 'ready');
  assert.equal(selectionActions({...summary, count: 201}, owner).delete.state, 'explain');
  const multiple = summarizeSelection([item('movie', 'a', 'one'), item('movie', 'b', 'two')]);
  assert.deepEqual(selectionActions(multiple, owner).delete, {state: 'disabled', reason: 'one-library-required'});
});

test('channels and people never inherit catalogue bulk operations, even from accidental all-true offers', () => {
  const channel: SelectionItem = {kind: 'channel', sourceId: 'source', channelId: 'channel', capabilities: selectionCapabilities('channel', all)};
  assert.equal(channel.capabilities.play, true); assert.equal(channel.capabilities.favorite, true);
  for (const name of ['queue', 'playlist', 'myList', 'watched', 'collection', 'download', 'edit', 'refresh', 'delete'] as const) assert.equal(channel.capabilities[name], false, name);
  const rules = selectionActions(summarizeSelection([channel]), owner);
  assert.equal(rules.play.state, 'ready'); assert.equal(rules.favorite.state, 'ready');
  for (const name of ['play-next', 'play-later', 'playlist', 'my-list', 'edit', 'delete', 'refresh', 'mark-watched'] as const) assert.equal(rules[name].state, 'hidden', name);
  const person: SelectionItem = {kind: 'person', id: 'person', capabilities: selectionCapabilities('person', all)};
  assert(Object.values(person.capabilities).every(value => value === false));
  assert(Object.values(selectionActions(summarizeSelection([person]), owner)).every(rule => rule.state === 'hidden'));
  const mixed = selectionActions(summarizeSelection([item('movie', 'm'), channel]), owner);
  assert.equal(mixed.play.state, 'hidden', 'guide playback has no mixed catalogue/channel queue');
  assert.equal(mixed.edit.state, 'hidden'); assert.equal(mixed.delete.state, 'hidden'); assert.equal(mixed['play-next'].state, 'hidden');
  const channels = selectionActions(summarizeSelection([channel, {...channel, channelId: 'second'}]), owner);
  assert.equal(channels.play.state, 'hidden'); assert.equal(channels.favorite.state, 'ready');
});

test('the episode fourth slot is Watched and an empty selection grants no action', () => {
  assert.equal(selectionFourthAction(summarizeSelection([item('episode', 'e')])), 'mark-watched');
  assert.equal(selectionFourthAction(summarizeSelection([item('movie', 'm')])), 'my-list');
  assert.equal(selectionFourthAction(summarizeSelection([item('episode', 'e'), item('song', 's')])), 'my-list');
  assert(Object.values(selectionActions(summarizeSelection([]), owner)).every(rule => rule.state === 'hidden'));
});
