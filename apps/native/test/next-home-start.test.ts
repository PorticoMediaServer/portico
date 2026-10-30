import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeDocument, HomeEntry, HomeRow} from '../../../packages/client-core/src/home';
import {createFocusRegistry, type FocusAddress} from '../src-next/kit/focus/registry';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry';
import {createTVPageBack} from '../src-next/screens/destinations/tv-page-back';
import {canYieldTVHomeMenu, homeMixesFocusRow, homeSpotlightFocusActions, homeStartModel, resolveTVHomeStart, type HomeMenuState} from '../src-next/screens/home/home-start';
import {tvFocusIds, tvHomeRegionMap} from '../src-next/shell/tv-focus';

// Actual registry and Back owner; host focus delivery is explicit and synchronous.
// These source vectors do not exercise a native remote, focus engine or Menu bridge.
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'opening', viewerFence: 'viewer'};
const ids = tvFocusIds(authority);
const entry: HomeEntry = {id: 'song', kind: 'song', libraryId: 'music', title: 'Offered song'};
const row: HomeRow = {id: 'music', title: 'Music', kind: 'ordinary', artworkShape: 'square', viewerFence: authority.viewerFence,
  endpoint: '/home/music', privacySensitivity: 'viewer', policyState: 'allowed', priority: 0, required: false, hideable: true,
  reorderable: true, critical: false, cursorCapable: false, entries: [entry], total: 1, start: 0, limit: 20, hasMore: false,
  nextCursor: '', revision: {catalog: 1, viewer: 1}};
function document(rows: readonly HomeRow[] = [], spotlight: HomeDocument['spotlight'] = []): HomeDocument {
  return {serverId: authority.serverId, viewerFence: authority.viewerFence, rows, spotlight,
    layout: {revision: 1, rowOrder: rows.map(value => value.id), hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-26T00:00:00Z'};
}
function fixture(value: HomeDocument, renderedRows = value.rows.map(item => item.id), mixesReady = false) {
  const registry = createFocusRegistry(), graph = tvHomeRegionMap(authority, renderedRows);
  registry.configure({...graph.spotlight, order: []});
  for (const region of graph.rows.values()) registry.configure(region);
  registry.configure({...graph.customize, order: ['customize']});
  const model = homeStartModel(value, authority, {mixesReady, playbackAvailable: true});
  let requests = 0;
  const attach = (address: FocusAddress) => registry.attach(address, {requestTVFocus() {requests++; registry.didFocus(address);}});
  const resolve = () => resolveTVHomeStart(registry, model);
  return {registry, graph, model, attach, resolve, requests: () => requests};
}
const clear: HomeMenuState = {home: true, visible: true, obscured: false, expanded: false, listening: false,
  scrolled: false, account: false, rail: false, chromeHidden: false, contentVisible: true, overlay: false};

test('resolved music-only Home uses its real first row; Back scrolls once and then yields', () => {
  const f = fixture(document([row])), start = {regionId: ids.row(row.id), childId: ids.entry(entry)};
  f.attach(start); f.attach(ids.last); f.registry.didFocus(ids.last);
  let top = false, scrolls = 0;
  const back = createTVPageBack(f.registry, {current: () => true, start: () => f.resolve().address,
    atTop: () => top && f.resolve().kind === 'ready', scrollTop: () => {scrolls++; top = true;}});
  assert.deepEqual(f.resolve(), {kind: 'ready', address: start});
  assert.equal(back.back(), true); assert.equal(scrolls, 1); assert.equal(f.requests(), 1);
  assert.deepEqual(f.registry.capture()?.childId, start.childId);
  assert.equal(back.back(), false); assert.equal(scrolls, 1);
  assert.equal(canYieldTVHomeMenu(f.resolve(), f.registry.capture(), clear), true);
  back.dispose();
});

for (const prior of [undefined, ids.last, {regionId: ids.row('removed'), childId: 'old'}]) test(`empty admitted Home has no target and yields with prior focus ${JSON.stringify(prior)}`, () => {
  const registry = createFocusRegistry(), model = homeStartModel(document(), authority);
  if (prior) registry.didFocus(prior);
  const resolve = () => resolveTVHomeStart(registry, model);
  let scrolls = 0;
  const back = createTVPageBack(registry, {current: () => true, start: () => resolve().address,
    atTop: () => resolve().kind === 'empty', scrollTop: () => {scrolls++;}});
  assert.deepEqual(resolve(), {kind: 'empty'}); assert.deepEqual(model.fallbacks, []);
  for (let index = 0; index < 8; index++) assert.equal(back.back(), false);
  assert.equal(scrolls, 0); assert.equal(registry.host(ids.customize), undefined); assert.equal(registry.child(ids.last), undefined);
  assert.equal(canYieldTVHomeMenu(resolve(), registry.capture(), clear), true);
  for (const state of [{home: false}, {visible: false}, {obscured: true}, {expanded: true}, {listening: true}, {scrolled: true},
    {account: true}, {rail: true}, {chromeHidden: true}, {contentVisible: false}, {overlay: true}]) {
    assert.equal(canYieldTVHomeMenu(resolve(), registry.capture(), {...clear, ...state}), false, JSON.stringify(state));
  }
  // Notifications after repeated Back must not reveal a pending focus jump.
  let requests = 0; registry.attach(ids.last, {requestTVFocus() {requests++;}});
  assert.equal(requests, 0); assert.deepEqual(resolve(), {kind: 'empty'}, 'old mounted controls cannot become an empty-page start');
  back.dispose();
});

test('admitted deferred-only descriptors are waiting, not an actionless empty Home', () => {
  const registry = createFocusRegistry(), model = homeStartModel(document([{...row, entries: [], deferred: true}]), authority);
  registry.configure({id: ids.customize, order: ['customize']}); registry.attach(ids.last, {requestTVFocus() {assert.fail('must not skip unresolved row');}});
  const start = resolveTVHomeStart(registry, model);
  assert.deepEqual(start, {kind: 'waiting'}); assert.equal(model.actionless, false);
  assert.equal(canYieldTVHomeMenu(start, undefined, clear), false);
});

test('nonplayable Spotlight starts at the actual Details action, never invented Play', () => {
  const details: HomeEntry = {...entry, homeActions: {play: false, queue: false, playlist: false, collection: false,
    myList: false, favorite: false, watched: false, played: false, rating: false, editMetadata: false, refreshMetadata: false,
    delete: false, download: false, details: true, state: {watchlisted: false, favorite: false, watched: false, rating: null,
      personalRevision: 1, containerRevision: 1}, selection: {play: false, queue: false, playlist: false, collection: false,
      myList: false, favorite: false, watched: false, download: false, edit: false, refresh: false, delete: false}}};
  const f = fixture(document([], [{entry: details, reason: {kind: 'featured', text: {code: 'featured', fallback: 'Featured'}}}]));
  const actions = homeSpotlightFocusActions(details, undefined, {platform: 'tv', playbackAvailable: true});
  assert.deepEqual(actions.order, ['details']);
  f.registry.configure({...f.graph.spotlight, order: actions.order});
  assert.equal(f.resolve().kind, 'waiting', 'an offered but unattached action is still deferred');
  const address = {regionId: ids.spotlight, childId: 'details'}; f.attach(address); f.registry.didFocus(address);
  assert.deepEqual(f.resolve(), {kind: 'ready', address});
  const back = createTVPageBack(f.registry, {current: () => true, start: () => f.resolve().address,
    atTop: () => f.resolve().kind === 'ready', scrollTop() {assert.fail('already at actual Details start');}});
  assert.equal(back.back(), false); assert.equal(canYieldTVHomeMenu(f.resolve(), f.registry.capture(), clear), true);
  back.dispose();
});

test('a genuinely virtualized first row waits rather than skipping to mounted Customize', () => {
  const f = fixture(document([row])); f.attach(ids.last); f.registry.didFocus(ids.last);
  let top = false, scrolls = 0;
  const back = createTVPageBack(f.registry, {current: () => true, start: () => f.resolve().address,
    atTop: () => top && f.resolve().kind === 'ready', scrollTop: () => {top = true; scrolls++;}});
  assert.equal(f.resolve().kind, 'waiting'); assert.equal(canYieldTVHomeMenu(f.resolve(), f.registry.capture(), clear), false);
  for (let index = 0; index < 8; index++) assert.equal(back.back(), true);
  assert.equal(scrolls, 1); assert.equal(f.requests(), 0);
  const address = {regionId: ids.row(row.id), childId: ids.entry(entry)};
  f.attach(address);
  assert.equal(f.requests(), 1); assert.equal(back.back(), false);
  back.dispose();
});

test('Home start follows the committed rendered row order and skips only explicitly disabled regions', () => {
  const later = {...row, id: 'later'};
  const f = fixture(document([later, row]), [row.id, later.id]);
  const firstAddress = {regionId: ids.row(row.id), childId: ids.entry(entry)};
  const laterAddress = {regionId: ids.row(later.id), childId: ids.entry(entry)};
  f.attach(firstAddress); f.attach(laterAddress); f.attach(ids.last);
  assert.deepEqual(f.resolve().address, firstAddress, 'latest document order cannot replace the actual retained Home order');
  f.registry.configure({...f.graph.rows.get(row.id)!, enabled: false});
  assert.deepEqual(f.resolve().address, laterAddress);
});

test('real mixes are a fallback before Customize and unresolved or foreign documents never yield', () => {
  const f = fixture(document(), [homeMixesFocusRow], true), address = {regionId: ids.row(homeMixesFocusRow), childId: 'mix'};
  f.attach(address); f.attach(ids.last); assert.deepEqual(f.resolve().address, address);
  for (const value of [undefined, {...document(), serverId: 'other'}, {...document(), viewerFence: 'other'}]) {
    const start = resolveTVHomeStart(f.registry, homeStartModel(value, authority, {mixesReady: true}));
    assert.equal(start.kind, 'waiting'); assert.equal(canYieldTVHomeMenu(start, address, clear), false);
  }
});

test('Menu yields only at that same actual Home start with all other owners absent', () => {
  const f = fixture(document([row])); f.attach(ids.last);
  f.registry.configure({...f.graph.rows.get(row.id)!, enabled: false});
  const start = f.resolve();
  assert.equal(canYieldTVHomeMenu(start, ids.last, clear), true);
  const blocked: Partial<HomeMenuState>[] = [{home: false}, {visible: false}, {obscured: true}, {expanded: true},
    {listening: true}, {scrolled: true}, {account: true}, {rail: true}, {chromeHidden: true}, {contentVisible: false}, {overlay: true}];
  for (const state of blocked) assert.equal(canYieldTVHomeMenu(start, ids.last, {...clear, ...state}), false, JSON.stringify(state));
  assert.equal(canYieldTVHomeMenu(start, undefined, clear), false);
  assert.equal(canYieldTVHomeMenu(start, {regionId: ids.rail, childId: 'home'}, clear), false);
  assert.equal(canYieldTVHomeMenu(start, {regionId: ids.row(row.id), childId: 'lower'}, clear), false);
});

test('initial Home entry uses the same deferred resolver and requests its actual row only once', () => {
  const f = fixture(document([row])), initial = createInitialRegionEntry(f.registry, ids.spotlight);
  const request = () => {const start = f.resolve(); return initial.request({active: start.kind === 'ready', primary: start.address?.regionId ?? ids.spotlight});};
  assert.equal(request(), 'waiting'); assert.equal(f.requests(), 0);
  const address = {regionId: ids.row(row.id), childId: ids.entry(entry)};
  f.attach(address); assert.equal(request(), 'requested'); assert.equal(f.requests(), 1);
  f.registry.didFocus(ids.last);
  assert.equal(request(), 'finished'); assert.equal(f.requests(), 1, 'later data/render cannot reclaim the starting row');
});

test('a hero without buttons makes Up on the first row a stop instead of leaving to the rail (E-tv-08)', () => {
  const withHero = tvHomeRegionMap(authority, ['a', 'b']), noHero = tvHomeRegionMap(authority, ['a', 'b'], {spotlightFocusable: false});
  assert.equal(withHero.rows.get('a')!.edges.up, ids.spotlight);
  assert.equal(noHero.rows.get('a')!.edges.up, undefined);
  assert.deepEqual(noHero.rows.get('a')!.stops, {up: true});
  assert.equal(noHero.rows.get('a')!.neighbors.previous, ids.spotlight, 'the focus-restore chain is unchanged');
  assert.equal(noHero.rows.get('b')!.edges.up, ids.row('a'));
  assert.equal(noHero.rows.get('b')!.stops, undefined);
});

test('a hero without actions and only skeleton rows waits instead of starting on Customize Home (E-tv-03)', () => {
  const skeleton: HomeRow = {...row, id: 'later', entries: [], deferred: true};
  const f = fixture(document([skeleton]), []);
  f.attach({regionId: ids.customize, childId: 'customize'});
  assert.equal(f.model.deferredPending, true);
  assert.equal(f.resolve().kind, 'waiting');
  // Once the rows have loaded the start is the first real row, never Customize.
  const loaded = fixture(document([row]));
  loaded.attach({regionId: ids.customize, childId: 'customize'});
  assert.equal(loaded.model.deferredPending, false);
});
