import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {SavedApi, SavedRoute, SavedScope} from '../../../packages/client-core/src/saved';
import type {NativeShellRuntime} from '../src-next/shell/runtime';
import type {NativeSavedEntryActions} from '../src-next/screens/saved/SavedResourceScreen';

/**
 * NM-ID-01 / BLK-TV-004 / FX-154 real-binding source. The standard-home loader
 * supplies the actual production React, native host boundary and module
 * resolution; fabric-host mounts the real SavedResourceScreen on the installed
 * Fabric reconciler. The page reads a real NativeSavedViewDestination and the
 * real PersonalSavedService, so scroll continuation and the owner visibility switch reach
 * the caller's own paged read and update receipt through the product callbacks.
 * The focus registry is the product one: this proves the switch attaches in
 * its actual page region and the scroller keeps its original offered cursor. Element state is
 * UNRUN here; no runtime/device claim.
 */

const scope: SavedScope = {serverId: 'server-one', viewerId: 'viewer-one'};
const route: SavedRoute = {view: 'resource', resourceId: 'collection-1'};
const authority = {serverId: 'server-one', accountId: 'account-one', profileId: 'profile-one', epoch: 'epoch-one', viewerFence: 'fence-one'};
const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;

type Call = {path: string; method: string; body?: unknown};
const movie = (id: string, title: string) => ({id, libraryId: 'movies', kind: 'movie', title, available: true, playback: {itemId: id, startSeconds: 0}});
const entry = (id: string, mediaId: string, title: string) => ({id, hidden: false, media: movie(mediaId, title)});

/** The canonical personal Saved server for one collection: a genuine second
 * page, and the PATCH receipt + refresh the real service validates. */
class CollectionFixture {
  readonly calls: Call[] = [];
  readonly requestIds: string[] = [];
  visibility: 'private' | 'server' = 'private';
  revision = 1;
  readonly api: SavedApi = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => this.route(path, method, body) as Promise<T>};
  private resource() {
    return {serverId: scope.serverId, viewerFence: 'fence-one', id: 'collection-1', kind: 'collection', name: 'Comfort movies',
      summary: '', visibility: this.visibility, revision: this.revision, role: 'owner', entryCount: 2,
      actions: ['update', 'pin', 'delete', 'entries', 'reorder'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 0, shares: []};
  }
  private async route(path: string, method: string, body?: unknown): Promise<unknown> {
    this.calls.push({path, method, ...(method === 'PATCH' ? {body} : {})});
    if (method === 'GET') {
      const cursor = new URL(path, 'https://saved.test').searchParams.get('cursor');
      // A collection page echoes its query and offers its orders and filters (P2-25).
      const page = (start: number) => ({query: {sort: 'position', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'},
        sorts: [{id: 'position', labelKey: 'sort.position', directions: ['asc']}], filters: [], totalCount: 2, start});
      if (cursor === null) return {resource: this.resource(), nextCursor: 'page-2', entries: [entry('e1', 'm1', 'First page title')], ...page(0), order: {next: 'e2', last: 'e2'}};
      if (cursor === 'page-2') return {resource: this.resource(), nextCursor: '', entries: [entry('e2', 'm2', 'Second page title')], ...page(1), order: {previous: 'e1', last: 'e2'}};
      throw new Error(`unexpected cursor ${cursor}`);
    }
    if (method === 'PATCH') {
      const change = body as {expectedRevision?: number; visibility?: 'private' | 'server'; operationId?: string};
      assert.equal(change.expectedRevision, this.revision, 'the page sends the revision it read');
      assert.match(String(change.operationId), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i);
      this.visibility = change.visibility ?? this.visibility;
      this.revision += 1;
      return {serverId: scope.serverId, viewerFence: 'fence-one',
        receipt: {operationId: change.operationId, revision: this.revision, resourceId: 'collection-1', deleted: false}};
    }
    throw new Error(`unexpected ${method} ${path}`);
  }
}

test('NM-ID-01 / MOB-050: the real Saved page continues its offered cursor on scroll and retains owner visibility focus', async () => {
  const app = await standardHomeApp({mountUI: false});
  const host = await fabricHost();
  try {
    const {createFocusRegistry, focusTestID} = await import('../src-next/kit/focus/registry.ts');
    const {FocusProvider} = await import('../src-next/kit/focus/FocusRegion.tsx');
    const {ThemeProvider} = await import('../src-next/kit/theme.ts');
    const {NativeSavedViewDestination} = await import('../src-next/screens/saved/view-model.ts');
    const {SavedResourceScreen} = await import('../src-next/screens/saved/SavedResourceScreen.tsx');
    const {defaultI18n} = await import('../../../packages/i18n/src/index.ts');

    const fixture = new CollectionFixture();
    const destination = new NativeSavedViewDestination({
      api: fixture.api, scope,
      requestId: async () => {const id = uuid(fixture.requestIds.length + 1); fixture.requestIds.push(id); return id;},
      current: () => true,
    });
    // useSyncExternalStore compares snapshots by identity: the port publishes one
    // stable activation object until a real modeled transition replaces it.
    const activationSnapshot = Object.freeze({visible: true, intent: 'enter' as const, revision: 1});
    const activation = {subscribe: () => () => {}, getSnapshot: () => activationSnapshot};
    const errors: unknown[] = [];
    const runtime = {
      getSnapshot: () => ({authority}), reportError: (error: unknown) => {errors.push(error);},
      error: (error: unknown, retry: () => void) => ({title: 'error', body: '', tone: 'error' as const,
        primaryAction: {id: 'retry', label: 'Retry', onPress: retry}}),
      text: defaultI18n, artwork: () => undefined, ambient: () => undefined,
    } as unknown as NativeShellRuntime;
    const actions: NativeSavedEntryActions = {open: () => {}, play: () => {}, menu: () => {}};
    const registry = createFocusRegistry();
    const region = 'saved.resource.resource:collection-1';
    const nodes = () => {
      const visit = (items: readonly any[]): any[] => items.flatMap(node => [node, ...visit(node.children ?? [])]);
      // standardHomeApp materialises the product's View/Text/Pressable boundaries in its own
      // host registry; host.root only carries the rendered raw text, whose string lives in props.text.
      return [...visit(host.root), ...app.hosts()];
    };
    const nodeWithTestID = (testID: string) => nodes().find(node => node.props?.testID === testID);
    const text = () => nodes().map(node => typeof node.children === 'string' ? node.children : typeof node.props?.text === 'string' ? node.props.text : '').join('\n');
    const settle = async () => {await host.settle(); await app.settle();};

    const page = (overrides: Record<string, unknown> = {}) => React.createElement(SavedResourceScreen, {
      route, destination, activation, runtime, actions, libraries: [], back: () => {}, ...overrides});
    const tree = (content: any) => React.createElement(ThemeProvider, {width: 390, children: React.createElement(FocusProvider, {registry, children: content})});

    try {
      await host.render(tree(page()));
      await settle();
      assert.equal(destination.getSnapshot().personal?.nextCursor, 'page-2', 'the genuine first page offers a second page');
      assert.match(text(), /First page title/);

      const showMoreID = focusTestID(region, 'saved-show-more');
      const visibilityID = focusTestID(region, 'saved-visibility');
      assert.equal(registry.child({regionId: region, childId: 'saved-show-more'}), undefined, 'MOB-050 replaces manual paging with scroll continuation');
      assert.ok(registry.child({regionId: region, childId: 'saved-visibility'}), 'the owner visibility switch attaches in the same actual region');
      const scroll = nodes().find(node => node.name === 'ScrollView' && node.props.onScroll && node.props.onContentSizeChange);
      assert.ok(scroll, 'the actual page scroller supplies continuation');
      const changeVisibility = nodeWithTestID(visibilityID);
      assert.equal(nodeWithTestID(showMoreID), undefined, 'there is no redundant manual paging control');
      assert.ok(changeVisibility, 'the visibility switch keeps its caller-region-derived host identity');
      assert.equal(changeVisibility!.props.accessibilityRole, 'switch');
      assert.equal(changeVisibility!.props.accessibilityState.checked, false, 'the loaded collection is private');

      const reads = fixture.calls.filter(call => call.method === 'GET').length;
      scroll.props.onContentSizeChange(390, 2400);
      scroll.props.onLayout({nativeEvent: {layout: {height: 600}}});
      await settle();
      assert.equal(fixture.calls.filter(call => call.method === 'GET').length, reads, 'the distant tail does not prefetch');
      scroll.props.onScroll({nativeEvent: {contentOffset: {y: 1600}}});
      await settle();
      const paged = fixture.calls.filter(call => call.method === 'GET').slice(reads);
      assert.equal(paged.length, 1, 'the near-tail scroll performs the real next-page read');
      assert.match(paged[0]!.path, /[?&]cursor=page-2(?:&|$)/, 'the caller page cursor is used, not a rebuilt query');
      assert.match(text(), /Second page title/, 'the real service publishes the next page');
      assert.equal(destination.getSnapshot().personal?.nextCursor, '', 'the last page retires continuation');
      scroll.props.onScroll({nativeEvent: {contentOffset: {y: 2000}}});
      await settle();
      assert.equal(fixture.calls.filter(call => call.method === 'GET').length, reads + 1, 'the old scroll callback cannot reuse the consumed cursor');

      await host.render(tree(page()));
      await settle();
      const switchNow = nodeWithTestID(visibilityID);
      assert.ok(switchNow);
      const writes = fixture.calls.filter(call => call.method === 'PATCH').length;
      switchNow!.props.onPress({nativeEvent: {}, stopPropagation() {}});
      await settle();
      const patches = fixture.calls.filter(call => call.method === 'PATCH').slice(writes);
      assert.equal(patches.length, 1, 'the switch dispatches one real personal update');
      assert.deepEqual(Object.keys(patches[0]!.body as object).sort(), ['expectedRevision', 'operationId', 'visibility']);
      assert.equal((patches[0]!.body as {visibility?: string}).visibility, 'server');
      assert.equal(destination.getSnapshot().personal?.resource?.visibility, 'server', 'the canonical updated resource is published');
      assert.equal(nodeWithTestID(visibilityID)!.props.accessibilityState.checked, true, 'the switch follows the confirmed resource');
      assert.deepEqual(errors, [], 'the real callbacks report no error');
    } finally {
      destination.dispose();
      await host.unmount();
    }
  } finally {
    try {
      await host.unmount();
    } finally {
      try {
        await app.unmount();
      } finally {
        app.disposeLoader();
      }
    }
  }
});
