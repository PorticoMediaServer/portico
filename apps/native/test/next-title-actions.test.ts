import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {refreshMetadataPath} from '../../../packages/client-core/src/presentation/entry-actions.ts';
import {nativeDownloadRequest} from '../src-next/screens/watch/download-request.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// FX-S3/FX-S4: the single-title actions behind the shared menu run on the existing fenced machinery.
const film = {id: 'film', kind: 'movie', title: 'Actual film', libraryId: 'library'} as any;
const show = {id: 'show', kind: 'show', title: 'Actual show', libraryId: 'library'} as any;
const deferred = <T = void>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};

async function load(role: 'owner' | 'member') {
  const h = hooks(), log: string[] = [], requests: [string, string | undefined][] = [], downloads: any[] = [], notices: string[] = [], reported: unknown[] = [];
  const detailReads: string[] = [];
  const detail = {granted: ['rating'], rating: null as number | null, pending: [] as unknown[], mutationError: null as unknown, mutations: [] as unknown[], disposed: 0};
  class FakeDetail {
    private target: any;
    async select(target: unknown, options?: {related?: string}) {this.target = target; log.push('detail-select'); detailReads.push(options?.related ?? 'all');}
    getSnapshot() {
      return {phase: 'ready', data: {scope: {libraryId: this.target.libraryId, itemId: this.target.itemId}, item: {kind: this.target.itemId === film.id ? 'movie' : 'show'},
        actions: detail.granted.map(id => ({id, enabled: true})), personal: {rating: detail.rating}}, pending: detail.pending, mutationError: detail.mutationError, error: null};
    }
    async mutate(intent: any) {detail.mutations.push(intent); if (intent.action === 'rating') detail.rating = intent.value;}
    async retryMutation() {log.push('retry');}
    dispose() {detail.disposed++;}
  }
  const destinations: any[] = [], bulks: any[] = [];
  class FakeDestinations {
    constructor(public options: any, public entries: any[]) {destinations.push(this); if (!options.isCurrent()) throw new Error('ended');}
    subscribe() {return () => {};}
    loaded: string[] = [];
    async load(kind: string) {this.loaded.push(kind);}
    snapshot() {return {busy: false};}
    disposed = false;
    dispose() {this.disposed = true;}
  }
  class FakeBulk {
    calls: string[] = [];
    disposed = false;
    constructor(public options: any, public entries: any[]) {bulks.push(this);}
    subscribe() {return () => {};}
    async openEditor() {this.calls.push('editor');}
    async openDelete() {this.calls.push('delete');}
    dispose() {this.disposed = true;}
  }
  const navigation = new Set<() => void>();
  let live = true;
  const context: any = {session: () => ({viewer: {role}}), current: () => live, authority: () => ({viewerFence: 'fence'}), subscribeAuthority: () => () => {},
    navigation: {subscribe: (listener: () => void) => {navigation.add(listener); return () => {navigation.delete(listener);};}},
    api: {request: async (path: string, method?: string) => {requests.push([path, method]); return {};}}};
  const {createNativeTitleActions} = await screenModule(new URL('../src-next/screens/watch/title-actions.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/detail': {DetailService: FakeDetail}, '../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../packages/client-core/src/presentation/entry-actions': {refreshMetadataPath}, '../../shell/selection-display': {selectedItemsLabel: () => '1 title'},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/selection': {BulkEditor: 'BulkEditor', DeleteMediaDialog: 'DeleteMediaDialog', RatingDialog: 'RatingDialog'}, '../../kit/theme': {useTheme: () => ({type: {}, colors: {}})},
    '../search/loaded-destinations': {NativeLoadedDestinations: FakeDestinations}, '../destinations/DestinationPicker': {NativeDestinationPicker: 'Picker'},
    '../destinations/bulk-media': {NativeBulkMedia: FakeBulk}, '../destinations/lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => live})},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => 'request', reportError: (error: unknown) => {reported.push(error);}, notice: (message: string) => {notices.push(message);}})},
    '../downloads/request': {askNativeDownload: (request: unknown) => {downloads.push(request);}}, './download-request': {nativeDownloadRequest},
  });
  const handle = createNativeTitleActions(context);
  const lifetime = () => {const abort = new AbortController(); let current = true; return {abort, end: () => {current = false; for (const listener of navigation) listener();}, value: {signal: abort.signal, isCurrent: () => current}};};
  const element = () => handle.render({} as any) as any;
  return {handle, detail, detailReads, destinations, bulks, requests, downloads, notices, reported, lifetime, element, log, retire: () => {live = false;}};
}

test('offer: Rate and Download are for everyone; collection and the owner rows follow the entry kind and the viewer\'s role', async () => {
  const owner = await load('owner'), member = await load('member');
  assert.deepEqual(owner.handle.offer(film), {rate: true, collection: true, edit: true, remove: true, refresh: true, download: true});
  assert.deepEqual(member.handle.offer(film), {rate: true, collection: true, edit: false, remove: false, refresh: false, download: true});
  const aggregate = owner.handle.offer(show);
  assert.equal(aggregate.collection, false, 'a show has no single-title collection add outside Home'); assert.equal(aggregate.edit, false); assert.equal(aggregate.remove, false);
  assert.equal(aggregate.refresh, true, 'the server refreshes a show as a whole');
  assert.equal(owner.handle.offer({...film, kind: 'album'}).refresh, false, 'an album cannot be refreshed');
  assert.equal(owner.handle.offer({...film, available: false}).collection, false);
});

test('Refresh metadata posts the title\'s own refresh route for an owner, under the opening, and never for a member or an ended opening', async () => {
  const owner = await load('owner'), first = owner.lifetime();
  await owner.handle.refreshMetadata(film, first.value);
  assert.deepEqual(owner.requests, [[refreshMetadataPath('movie', 'film'), 'POST']]);
  assert.deepEqual(owner.notices, [defaultI18n.t('next.selection.saved')]);
  await owner.handle.refreshMetadata(show, first.value);
  assert.equal(owner.requests.at(-1)![0], refreshMetadataPath('show', 'show'));
  const ended = owner.lifetime(); ended.end(); const before = owner.requests.length;
  await owner.handle.refreshMetadata(film, ended.value);
  assert.equal(owner.requests.length, before, 'an ended opening sends nothing');
  const member = await load('member');
  await member.handle.refreshMetadata(film, member.lifetime().value);
  assert.deepEqual(member.requests, []);
});

test('Refresh metadata retains the target captured before checking the opening', async () => {
  const owner = await load('owner'), entry = {...film};
  await owner.handle.refreshMetadata(entry, {signal: new AbortController().signal, isCurrent: () => {
    entry.id = 'replacement';
    return true;
  }});
  assert.deepEqual(owner.requests, [[refreshMetadataPath('movie', 'film'), 'POST']]);
});

test('Download raises the title page\'s own download request under the menu\'s opening', async () => {
  const f = await load('member'), opening = f.lifetime();
  f.handle.download(film, opening.value);
  assert.equal(f.downloads.length, 1); assert.deepEqual(f.downloads[0].itemId, 'film'); assert.equal(f.downloads[0].lifetime, opening.value);
  opening.end(); f.handle.download(film, opening.value);
  assert.equal(f.downloads.length, 1, 'an ended opening raises nothing');
});

test('Rate… reads the title\'s own Detail, offers the stars only on a rating grant, writes through Detail and ends with its opening', async () => {
  const f = await load('member'), opening = f.lifetime();
  f.handle.rate(film, opening.value);
  assert.equal(f.element().props.view().rating.phase, 'loading');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.element().props.view().rating.phase, 'ready');
  assert.equal(f.element().props.view().rating.value, null);
  assert.deepEqual(f.detailReads, ['none'], 'a film\'s Rate asks for Detail as its page does (no related rows), so it shares that read');
  f.element().props.rate.onRate(8);
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(f.detail.mutations, [{action: 'rating', value: 8}]);
  const done = f.element().props.view().rating;
  assert.equal(done.phase, 'result'); assert.equal(done.value, 8);
  opening.end();
  assert.equal(f.element().props.view(), undefined, 'the dialog closes with the opening');
  assert.equal(f.detail.disposed, 1);
  const refused = await load('member'); refused.detail.granted = [];
  const second = refused.lifetime(); refused.handle.rate(film, second.value);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(refused.element().props.view().rating.phase, 'error', 'a withheld rating grant never shows the stars');
  refused.element().props.rate.onRate(6);
  assert.deepEqual(refused.detail.mutations, [], 'nothing is written without a ready, granted rating');
  const uncertain = await load('member'), third = uncertain.lifetime();
  uncertain.handle.rate(film, third.value); await new Promise(resolve => setImmediate(resolve));
  uncertain.detail.pending = [{intent: {action: 'rating', value: 4}, phase: 'sent'}];
  uncertain.element().props.rate.onRate(4); await new Promise(resolve => setImmediate(resolve));
  assert.equal(uncertain.element().props.view().rating.phase, 'uncertain', 'an unconfirmed write is never shown as saved');
  uncertain.detail.pending = [];
  uncertain.element().props.rate.onRetry(); await new Promise(resolve => setImmediate(resolve));
  assert.equal(uncertain.element().props.view().rating.phase, 'result');
});

test('Add to collection opens the existing destination picker for exactly this title and closes with its opening', async () => {
  const f = await load('member'), opening = f.lifetime();
  f.handle.addToCollection(film, opening.value);
  assert.equal(f.destinations.length, 1);
  assert.deepEqual(f.destinations[0].entries, [film]); assert.deepEqual(f.destinations[0].loaded, ['collection']);
  assert.equal(f.destinations[0].options.viewerFence, 'fence');
  assert.equal(f.destinations[0].options.isCurrent(), true);
  opening.end();
  assert.equal(f.destinations[0].disposed, true, 'the picker and its writes end with the menu\'s opening');
  assert.equal(f.destinations[0].options.isCurrent(), false);
  const ended = f.lifetime(); ended.end(); f.handle.addToCollection(film, ended.value);
  assert.equal(f.destinations.length, 1, 'a retained row from an ended opening opens nothing');
});

test('Edit metadata and Delete open the bulk machinery for this one title, for an owner only, and a newer action replaces the older', async () => {
  const f = await load('owner'), opening = f.lifetime();
  f.handle.editMetadata(film, opening.value);
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(f.bulks[0].entries, [film]); assert.deepEqual(f.bulks[0].calls, ['editor']);
  f.handle.deleteTitle(film, opening.value);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.bulks[0].disposed, true, 'one action is open at a time'); assert.deepEqual(f.bulks[1].calls, ['delete']);
  opening.end(); assert.equal(f.bulks[1].disposed, true);
  const member = await load('member');
  member.handle.editMetadata(film, member.lifetime().value); member.handle.deleteTitle(film, member.lifetime().value);
  assert.equal(member.bulks.length, 0, 'a member can never open the owner\'s editor or delete');
  assert.equal(member.reported.length, 2);
});
