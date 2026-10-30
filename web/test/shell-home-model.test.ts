import test from 'node:test';
import assert from 'node:assert/strict';
import {createI18n} from '../../packages/i18n/src/index.ts';
import type {HomeDocument, HomeEntry, HomeLive, HomeRow} from '../../packages/client-core/src/home.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';
import {homeCollectionCaption, homeTitleFactParts} from '../../packages/client-core/src/presentation/home-spotlight.ts';
import {desktopLibraries, rememberedLibrary, rememberLibrary, libraryMemoryKey, shellGutter, showOwnerActivity, browserOwnsBackTransition} from '../src/shell/model.ts';
import {currentHomeLive, homeLiveDeadline, homeCardModel, homePlaybackItem, homePlaybackTarget, homeRows, homeSpotlights} from '../src/screens/home/model.ts';

const scope = {serverId: 'server-one', accountId: 'account-one', profileId: 'profile-one', authority: 'local'};
const libraries = Array.from({length: 8}, (_, index) => ({id: String(index), name: `${f.libraryPrefix} ${index}`, kind: 'movies'}));
const env = {t: createI18n().t, locale: 'en-US'};
function memory() {
  const data = new Map<string, string>();
  return {getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => { data.set(key, value); }, removeItem: (key: string) => { data.delete(key); }};
}
test('navigation uses the exact entitled names and server order, overflowing only after six', () => {
  const input = [...libraries].reverse();
  const {visible, overflow} = desktopLibraries(input);
  assert.deepEqual([...visible, ...overflow], input);
  assert.equal(visible.length, 6);
  assert.equal(overflow[0], input[6]);
  assert.deepEqual(desktopLibraries(input.slice(0, 2)).overflow, []);
});
test('Libraries remembers only a current entitled library in the exact server/account/profile authority', () => {
  const storage = memory();
  assert.equal(rememberedLibrary(storage, scope, libraries), undefined);
  rememberLibrary(storage, scope, libraries, '7');
  assert.equal(rememberedLibrary(storage, scope, libraries), '7');
  for (const other of [{...scope, serverId: 'server-two'}, {...scope, accountId: 'account-two'}, {...scope, profileId: 'profile-two'}, {...scope, authority: 'hosted'}]) {
    assert.notEqual(libraryMemoryKey(other), libraryMemoryKey(scope));
    assert.equal(rememberedLibrary(storage, other, libraries), undefined);
  }
  assert.equal(rememberedLibrary(storage, scope, libraries.slice(0, 7)), undefined);
  assert.equal(storage.getItem(libraryMemoryKey(scope)), null);
  rememberLibrary(storage, scope, libraries, 'removed');
  assert.equal(storage.getItem(libraryMemoryKey(scope)), null);
});
test('storage refusal leaves navigation usable and never chooses the first library', () => {
  const storage = {getItem() { throw new Error('storage refused'); }, setItem() { throw new Error('storage refused'); }, removeItem() { throw new Error('storage refused'); }};
  assert.equal(rememberedLibrary(storage, scope, libraries), undefined);
  assert.doesNotThrow(() => rememberLibrary(storage, scope, libraries, '2'));
});
test('Activity never mounts for administrators, members, or phone owners', () => {
  assert.equal(showOwnerActivity('owner', true), true);
  for (const role of ['administrator', 'admin', 'member', 'unknown']) assert.equal(showOwnerActivity(role, true), false);
  assert.equal(showOwnerActivity('owner', false), false);
  assert.deepEqual([390, 600, 1023, 1024, 1232, 1440, 1920].map(shellGutter), [20, 20, 20, 32, 48, 64, 64]);
});
test('browser-owned traversal suppresses Portico transitions without suppressing ordinary navigation', () => {
  assert.equal(browserOwnsBackTransition({hasUAVisualTransition: true}, {iphoneSafari: false, traversal: true}), true);
  assert.equal(browserOwnsBackTransition({}, {iphoneSafari: true, traversal: true}), true);
  assert.equal(browserOwnsBackTransition({}, {iphoneSafari: true, traversal: false}), false);
  assert.equal(browserOwnsBackTransition({}, {iphoneSafari: false, traversal: true}), false);
});
function liveAt(expiresAt = '2026-10-22T12:10:00Z'): HomeLive {
  return {revision: 'live-revision', observedAt: '2026-10-22T12:00:00Z', expiresAt, sort: 'favorites', group: '', groups: [], channels: [{
    source: {id: 'source', name: f.source, generation: '1', publishedAt: '', availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: ''},
    channel: {id: 'channel', sourceId: 'source', provenance: 'live-source', name: f.channel, number: '1', group: '', generation: '1', tuneAvailable: true, recordAvailable: false, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: true, hidden: false, preferenceRevision: 1, mark: {text: 'C', seed: 'source:channel'}, programmes: [
      {id: 'programme', channelId: 'channel', title: f.programme, start: '2026-10-22T11:55:00Z', end: '2026-10-22T12:05:00Z', lineage: 'provider-id', seriesId: '', episodeId: '', newEvidence: 'unknown', description: ''},
    ]},
  }]};
}
test('live facts expire exactly at the server fence and refresh at the earlier programme boundary', () => {
  const now = Date.parse('2026-10-22T12:02:00Z'), live = liveAt();
  assert.equal(currentHomeLive(live, now), live);
  assert.equal(currentHomeLive(live, Date.parse(live.expiresAt)), undefined);
  assert.equal(currentHomeLive(live, Date.parse(live.observedAt) - 1), undefined);
  assert.equal(currentHomeLive(live, NaN), undefined);
  assert.equal(homeLiveDeadline([{live} as HomeRow], now), Date.parse('2026-10-22T12:05:00Z'));
  assert.equal(homeLiveDeadline([{live: liveAt('2026-10-22T12:04:00Z')} as HomeRow], now), Date.parse('2026-10-22T12:04:00Z'));
  assert.equal(homeLiveDeadline([{live} as HomeRow], Date.parse(live.expiresAt)), undefined);
});
test('Home captions use actual structured episode facts and ignore the old joined subtitle', () => {
  const model = homeCardModel({id: 'episode', kind: 'episode', title: f.episode, subtitle: f.wrongSubtitle, show: {id: 'show', name: f.show}, seasonNumber: 1, episodeNumber: 2, duration: 1200, progressSeconds: 300}, true, env);
  assert.equal(model.title, f.show);
  assert.equal(model.progress, .25);
  assert.ok(model.caption.some(part => part.text === 'S1 E2'));
  assert.ok(model.caption.some(part => part.text === f.episode));
  assert.ok(model.detail.some(part => part.text.includes('15')));
  assert.ok(!JSON.stringify(model).includes('Wrong'));
});
test('Home keeps the server spotlight order, reason and resume target without client composition', () => {
  const reason = {kind: 'because', text: {code: 'unknown', fallback: f.reason}};
  const entry = {id: 'show', kind: 'show' as const, title: f.show, seasonCount: 3};
  const resumeTarget = {id: 'episode', kind: 'episode' as const, title: f.episode, seasonNumber: 2, episodeNumber: 4, progressSeconds: 100, playback: {itemId: 'file', startSeconds: 100}};
  const document = {spotlight: [{entry, reason, resumeTarget}, {entry: {...entry, id: 'other'}, reason}]} as HomeDocument;
  const models = homeSpotlights(document, env);
  assert.deepEqual(models.map(model => model.id), ['show', 'other']);
  assert.equal(models[0].primary?.entry, resumeTarget);
  assert.equal(models[0].reason, reason);
  assert.equal(models[0].primary?.label.key, 'next.home.resumeEpisode');
  assert.equal(models[1].primary, undefined);
});
test('Home Play requires an actual offer and keeps the displayed identity separate from the actual resume child', () => {
  const displayed: HomeEntry = {id: 'show', kind: 'show', title: f.show, libraryId: 'library', homeActions: {play: true} as HomeEntry['homeActions']};
  const resume: HomeEntry = {id: 'episode', kind: 'episode', title: f.episode, playback: {itemId: 'file', startSeconds: 37.25}};
  assert.equal(homePlaybackTarget(displayed, resume), resume.playback);
  assert.equal(displayed.id, 'show');
  assert.equal(homePlaybackTarget({...displayed, homeActions: undefined}, resume), undefined);
  assert.equal(homePlaybackTarget({...displayed, homeActions: {...displayed.homeActions!, play: false}}, resume), undefined);
  assert.equal(homePlaybackTarget(displayed, {...resume, available: false}), undefined);
  assert.equal(homePlaybackTarget(displayed, {...resume, missing: true}), undefined);
});
test('single Home Play rejects stale rendered pairs before the real fenced queue runtime is called', () => {
  const entry: HomeEntry = {id: 'show', kind: 'show', title: f.show, libraryId: 'library', homeActions: {play: true} as HomeEntry['homeActions']};
  const target: HomeEntry = {id: 'episode', kind: 'episode', title: f.episode, playback: {itemId: 'real-file', startSeconds: 40}};
  const document = {spotlight: [{entry, resumeTarget: target}], rows: []} as unknown as HomeDocument;
  const item = homePlaybackItem(document, entry, target)!;
  assert.equal(item.identity.kind, 'show'); assert.equal('id' in item.identity && item.identity.id, 'show');
  assert.ok('target' in item); assert.equal(item.target, target.playback);
  assert.equal(homePlaybackItem(document, {...entry}, target), undefined);
  assert.equal(homePlaybackItem(document, entry, {...target}), undefined);
  assert.equal(homePlaybackItem({...document, spotlight: []}, entry, target), undefined);
});
test('row order, geometry, causal facts and rank offsets remain server-authored', () => {
  const rows = [
    {id: 'community_watching', start: 4, kind: 'rail', artworkShape: 'poster', title: f.rankHeading, entries: [{id: 'one', kind: 'movie', title: f.movie}], period: {days: 30}, platforms: ['web']},
    {id: 'listening', start: 0, kind: 'rail', artworkShape: 'square', title: f.audioHeading, entries: [], platforms: ['web']},
    {id: 'tv-only', entries: [], platforms: ['tv']},
  ] as unknown as HomeRow[];
  const shown = homeRows({rows} as HomeDocument);
  assert.deepEqual(shown.map(row => row.id), ['community_watching', 'listening']);
  assert.deepEqual(shown.map(row => row.artworkShape), ['poster', 'square']);
  assert.equal(shown[0].cards[0].rank, 5);
});
test('Home hero facts format the displayed parent with canonical order and preserve the separate playback target', () => {
  const entry = {id: 'show', kind: 'show' as const, title: f.show, year: 2022, genres: [f.genre, f.secondGenre], seasonCount: 3, contentRating: 'TV-14'};
  const target = {id: 'episode', kind: 'episode' as const, title: f.episode, duration: 1200, playback: {itemId: 'file', startSeconds: 120}};
  const document = {spotlight: [{entry, resumeTarget: target, reason: {kind: 'featured', text: {code: 'featured', fallback: f.reason}}}]} as HomeDocument;
  const slide = homeSpotlights(document, env)[0];
  assert.deepEqual(slide.facts.map(part => [part.id, part.role]), [['year', 'value'], ['genres', 'supplement'], ['season-count', 'supplement']]);
  assert.equal(slide.facts[1].text, `${f.genre}, ${f.secondGenre}`);
  assert.equal(slide.primary?.entry, target); assert.equal(slide.entry, entry);
  assert.ok(!slide.facts.some(part => part.text.includes('TV-14') || part.id === 'duration'));
  const focusedEpisode = {...target, show: {id: 'show', name: f.show}};
  assert.deepEqual(homeTitleFactParts(focusedEpisode, env), []);
  const parentFacts = homeTitleFactParts({...focusedEpisode, year: 2026, genres: [f.secondGenre], homeTitleContext: {kind: 'show', year: 2022, genres: [f.genre], seasonCount: 3}}, env);
  assert.deepEqual(parentFacts.map(part => part.id), ['year', 'genres', 'season-count']);
  assert.equal(parentFacts[0].text, '2022'); assert.equal(parentFacts[1].text, f.genre);
  assert.ok(!parentFacts.some(part => part.id === 'duration' || part.text === '2026'));
});
test('Home collection caption consumes authoritative collection facts and never sweeps decorative members', () => {
  const collection = {collection: {itemCount: 4}, mediaKind: 'movie', firstYear: 1995, lastTitle: f.lastFilm};
  const caption = homeCollectionCaption(collection, env);
  assert.equal(caption.length, 1); assert.equal(caption[0].id, 'collection-summary');
  assert.ok(caption[0].text.includes('Four films')); assert.ok(caption[0].text.includes('1995')); assert.ok(caption[0].text.includes(f.lastFilm));
  assert.ok(!homeCollectionCaption({...collection, firstYear: undefined}, env)[0].text.includes(f.lastFilm));
  assert.deepEqual(homeCollectionCaption({...collection, mediaKind: undefined}, env), []);
});
