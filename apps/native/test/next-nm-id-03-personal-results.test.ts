import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import type {SearchGroup} from '../../../packages/client-core/src/search';
import type {NativeSearchDestination} from '../src-next/screens/search/model';

/**
 * NM-ID-03 / BLK-TV-005 / FX-155 personal-result source. The standard-home app
 * supplies the actual bootstrapped authority, real module resolution and the
 * native transport hook; the catalogue answers travel through that same hook,
 * so the actual Saved/Search selection owners use their normal context.api.
 * fabric-host mounts the product bars and Dialog on the installed Fabric
 * reconciler with the TV focus registry. A real failed personal write, its
 * same-operation Retry, and the accepted result are exercised. Element state is
 * UNRUN here.
 */

const fence = 'b'.repeat(64);
const movie = (id: string, title: string) => ({id, libraryId: 'movies', kind: 'movie' as const, title, available: true});
const detail = (serverId: string, entry: {id: string; title: string}, revision: number, watchlisted: boolean) => ({
  scope: {serverId, libraryId: 'movies', itemId: entry.id, viewerFence: fence},
  revision: {catalog: 1, viewer: 1},
  item: {id: entry.id, libraryId: 'movies', kind: 'movie', title: entry.title, durationMs: 600000, progressMs: 0, available: true},
  personal: {revision, watchlisted, favorite: false, rating: null, watched: false, progressSeconds: 0, lastPlayedAt: '', status: 'current', conflicts: []},
  actions: [{id: 'watchlist', labelKey: 'action.myList', enabled: true}, {id: 'favorite', labelKey: 'action.favorite', enabled: true}, {id: 'watched', labelKey: 'action.markWatched', enabled: true}],
  metadata: {status: 'available'}, markers: [],
});

test('NM-ID-03: a failed Saved personal result keeps the same operation on Retry and Done closes it', async t => {
  t.mock.timers.enable({apis: ['Date'], now: Date.now()});
  const entry = movie('m1', 'Selected movie');
  let failWrite = true, revision = 1;
  const app = await standardHomeApp({accountRole: 'owner', mountUI: false, nativeResponse: async (request: any, fixture: any) => {
    // The actual selection owners reach this hook through their own context.api.
    if (request.path.startsWith(`/v1/items/${entry.id}/detail`) && request.method === 'GET')
      return {status: 200, body: detail(fixture.pin.serverId, entry, revision, revision > 1)};
    if (request.path === `/v1/items/${entry.id}/personal-state` && request.method === 'PUT') {
      if (failWrite) return {status: 500, body: {error: {code: 'unavailable', message: 'synthetic personal failure'}}};
      revision = 2;
      return {status: 200, body: {operationId: request.body.operationId, serverId: fixture.pin.serverId, libraryId: 'movies',
        itemId: entry.id, viewerFence: fence, current: detail(fixture.pin.serverId, entry, 2, true).personal}};
    }
    return undefined;
  }});
  const host = await fabricHost();
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {createFocusRegistry, focusTestID} = await import('../src-next/kit/focus/registry.ts');
    const {FocusProvider} = await import('../src-next/kit/focus/FocusRegion.tsx');
    const {ThemeProvider} = await import('../src-next/kit/theme.ts');
    const {Platform} = await import('react-native');
    const {createNativeSavedSelection} = await import('../src-next/screens/saved/selection.ts');
    const {createNativeSearchSelection} = await import('../src-next/screens/search/selection.ts');
    const {selectionText} = await import('../src-next/kit/selection/copy.ts');

    let context: NativeShellBootstrapContext | undefined;
    runtime = await restoreNativeShellRuntime(next => {context = next; return createNativeShellBindings(next);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    assert.ok(context);
    const booted = context;
    const priorTV = Platform.isTV;
    (Platform as {isTV: boolean}).isTV = true;
    const registry = createFocusRegistry();
    // One stable activation snapshot, replaced only by a modeled transition.
    const activationSnapshot = Object.freeze({visible: true, intent: 'enter' as const, revision: 1});
    const activation = {subscribe: () => () => {}, getSnapshot: () => activationSnapshot};
    // One stable Saved publication: start(rendered) requires now.myList===rendered.
    const projection = {
      scope: {serverId: booted.authority().serverId, libraryId: '', libraryKind: 'mixed', view: 'watchlist', entityId: 'watchlist', viewerFence: fence},
      revision: {catalog: 1, viewer: 1}, playlistRevision: 0,
      heading: {key: 'saved.watchlist', fallback: 'Watchlist'}, navigation: [],
      query: {sort: 'title', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [],
      sections: [{id: 'entries', type: 'list', heading: {key: 'saved.entries', fallback: 'Entries'}, entries: [entry], totalCount: 1, nextCursor: ''}],
    };
    const myList = Object.freeze({projection: Object.freeze(projection), route: Object.freeze({view: 'watchlist' as const})});
    const savedSnapshot = Object.freeze({phase: 'ready' as const, myList, favorites: Object.freeze({})});
    const savedSource = {subscribe: () => () => {}, getSnapshot: () => savedSnapshot};
    const saved = createNativeSavedSelection(booted, savedSource as never, activation);
    assert.equal(typeof saved.start, 'function', 'the Saved owner exposes its actual start seam');
    saved.start!(savedSnapshot.myList as never);
    const beforePick = saved.item(entry);
    assert.ok(beforePick, 'the started surface exposes the exact page entry');
    beforePick!.onEnter();
    const nodes = () => {
      const visit = (items: readonly any[]): any[] => items.flatMap(node => [node, ...visit(node.children ?? [])]);
      // standardHomeApp materialises the product's View/Text/Pressable boundaries in its own
      // host registry; host.root only carries the rendered raw text.
      return [...visit(host.root), ...app.hosts()];
    };
    const nodeWithTestID = (testID: string) => nodes().find(node => node.props?.testID === testID);
    const byLabel = (value: string) => nodes().find(node => node.name === 'Pressable' && app.nativeLabel(node) === value);
    const press = (node: any) => node.props.onPress({nativeEvent: {}, stopPropagation() {}});
    const settle = async () => {await host.settle(); await app.settle();};
    try {
      await host.render(React.createElement(ThemeProvider, {width: 1920, children: React.createElement(FocusProvider, {registry, children:
        saved.render(() => null)})}));
      await settle();
      const afterPick = saved.item(entry);
      assert.equal(afterPick?.selected, true, 'the real catalogue detail read and toggle selected the page entry');
      assert.equal(afterPick?.active, true);
      const myListAction = byLabel(selectionText('next.selection.native.action.myList'));
      assert.ok(myListAction, 'the actual Saved selection bar offers My List');
      press(myListAction);
      await settle();
      assert.ok(nodeWithTestID('saved-personal-results'), 'the Saved result sheet is the actual product Dialog');
      assert.ok(nodeWithTestID(focusTestID('saved-personal-results', 'done')), 'Done keeps its footer identity');
      // A server 500 has an ambiguous outcome: the durable owner first retries
      // the same command, then exposes its pending error after five attempts.
      for (let attempt = 0; attempt < 4; attempt++) {
        t.mock.timers.tick(60_000);
        await booted.api.getUserMutationCoordinator()!.reconcile();
        await settle();
      }
      const retry = nodeWithTestID(focusTestID('saved-personal-results', 'retry'));
      assert.ok(retry, 'a failed write offers the actual Retry leaf');
      const puts = () => app.requests.filter((request: any) => request.method === 'PUT' && request.path === `/v1/items/${entry.id}/personal-state`);
      assert.equal(puts().length, 5);
      assert.ok(app.receipts.some((receipt: any) => receipt.method === 'PUT' && receipt.path === `/v1/items/${entry.id}/personal-state` && receipt.status === 500));
      const firstBody = puts()[0].body;
      assert.equal(firstBody.watchlisted, true);
      assert.equal(firstBody.expectedRevision, 1);
      failWrite = false;
      press(retry);
      t.mock.timers.tick(60_000);
      await booted.api.getUserMutationCoordinator()!.reconcile();
      await settle();
      assert.equal(puts().length, 6, 'Retry re-sends the captured original operation');
      assert.ok(puts().every((request: any) => JSON.stringify(request.body) === JSON.stringify(firstBody)), 'all physical retries preserve the original body');
      assert.equal(puts()[1].body.operationId, firstBody.operationId, 'the same operation identity is retained');
      assert.deepEqual(puts()[1].body, firstBody, 'the retried target/body are the captured original');
      assert.equal(nodeWithTestID(focusTestID('saved-personal-results', 'retry')), undefined, 'the accepted retry retires Retry');
      press(nodeWithTestID(focusTestID('saved-personal-results', 'done'))!);
      await settle();
      assert.equal(nodeWithTestID('saved-personal-results'), undefined, 'Done closes the actual result sheet');
      saved.dispose();

      // Search owns a second real selection surface over the same transport hook.
      const group = 'movies' as SearchGroup;
      const result = Object.freeze({id: 'movies', status: 'success' as const, items: Object.freeze([entry]), total: 1});
      const searchSnapshot = Object.freeze({generation: 1, scope: Object.freeze({serverId: booted.authority().serverId, viewerId: 'viewer-one'}),
        query: Object.freeze({q: 'movie'}), phase: 'ready' as const,
        data: Object.freeze({scope: Object.freeze({serverId: booted.authority().serverId, authority: 'local' as const, accountId: 'account', profileId: 'profile', viewerFence: fence}),
          revision: Object.freeze({catalog: 1, viewer: 1}), query: 'movie'}),
        groups: Object.freeze([result]), pagination: Object.freeze({cursor: null, canPrevious: false}), error: null});
      const searchSourceSnapshot = Object.freeze({search: searchSnapshot, groups: Object.freeze([result])});
      const searchSource = {subscribe: () => () => {}, getSnapshot: () => searchSourceSnapshot};
      const search = createNativeSearchSelection(booted, searchSource as unknown as NativeSearchDestination, activation);
      search.register(searchSnapshot as never, [result] as never);
      const searchBefore = search.item(entry, group);
      assert.ok(searchBefore, 'the registered Search surface exposes the exact result entry');
      searchBefore!.onEnter();
      await host.render(React.createElement(ThemeProvider, {width: 1920, children: React.createElement(FocusProvider, {registry, children:
        search.render(() => null)})}));
      await settle();
      assert.equal(search.item(entry, group)?.selected, true, 'the real Search catalogue read and toggle selected the result');
      const searchMyList = byLabel(selectionText('next.selection.native.action.myList'));
      assert.ok(searchMyList, 'the actual Search selection bar offers My List');
      press(searchMyList);
      await settle();
      assert.ok(nodeWithTestID('search-personal-results'), 'the Search result sheet is the actual product Dialog');
      assert.ok(nodeWithTestID(focusTestID('search-personal-results', 'done')));
      press(nodeWithTestID(focusTestID('search-personal-results', 'done'))!);
      await settle();
      assert.equal(nodeWithTestID('search-personal-results'), undefined);
      search.dispose();
    } finally {
      (Platform as {isTV: boolean}).isTV = priorTV;
    }
  } finally {
    try {
      await host.unmount();
    } finally {
      try {
        await app.unmount();
      } finally {
        try {
          app.disposeLoader();
        } finally {
          runtime?.dispose();
        }
      }
    }
  }
});
