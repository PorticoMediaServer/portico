import * as pagingModule from '../src-next/screens/saved/paging.ts';
import * as savedCore from '../../../packages/client-core/src/saved.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];

/** The actual screen callbacks, with explicit canonical mutation/retry ports.
 * The service owns uncertain operation bytes; this screen may only call Retry. */
async function fixture() {
  const h = hooks(), react = {...h.react, useId: () => 'resource-instance', useLayoutEffect() {}}; react.default = react;
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  const theme = {isTV: true, gutter: 96, textScale: 1, colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', raised: '#222', line: '#333'}, type: {body: {}, caption: {}}};
  const relative = '../../../../../../packages';
  const hook = await screenModule(new URL('../src-next/pages/shared/saved/useSavedResource.ts', import.meta.url), {
    react, [`${relative}/i18n/src`]: {defaultI18n: {t: (key: string) => key, has: () => true}},
    [`${relative}/client-core/src/presentation/errors`]: {presentError: () => ({body: 'Retry this saved change'})},
    [`${relative}/client-core/src/saved`]: savedCore,
    '../../../shell/model': {sameAuthority: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)},
    '../../../screens/read/platform': {isReadingLibraryKind: () => false},
    '../../../screens/saved/presentation': {exactDurationSeconds: () => undefined, personalMosaic: () => [], playlistEntryMosaic: () => [],
      savedDuration: String, savedItemCount: String, savedLibraryName: () => 'Movies', savedPlaylistEyebrow: 'Playlist',
      savedResourceEyebrow: () => 'Collection', savedTitleCount: String, savedViewConditionCount: () => 0,
      savedVisibility: () => ({text: 'Private'}), savedVisibilityHelp: () => ''},
    '../../../screens/saved/paging': pagingModule,
    [`${relative}/client-core/src/presentation/kit-model/text`]: {durationText: () => '4 min'},
  });
  const component = (name: string) => name;
  const {SavedResourcePage} = await screenModule(new URL('../src-next/pages/tv/saved/SavedResourcePage.tsx', import.meta.url), {
    react, [`${relative}/i18n/src`]: {defaultI18n: {t: (key: string) => key, number: String}},
    [`${relative}/client-core/src/saved`]: {}, [`${relative}/client-core/src/presentation/kit-model/text`]: {durationText: () => '4 min'},
    '../../../kit/artwork': {AmbientArt: component('AmbientArt'), CoverMosaic: component('CoverMosaic')},
    '../../../kit/browse': {ChipRail: component('ChipRail')}, '../../../kit/controls': {Chip: component('Chip')},
    '../../../kit/controls/KeyHints': {KeyHints: component('KeyHints')},
    '../../../kit/frames': {musicScroller: () => 'scroller', PanelBlock: component('PanelBlock'), PosterGridWindow: component('PosterGridWindow'),
      StateRow: component('StateRow'), TitleFrame: component('TitleFrame'), TitleRowWindow: component('TitleRowWindow')},
    '../../../kit/layout': {HeroMetadata: component('HeroMetadata')},
    '../../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet', Menu: 'Menu'},
    '../../../kit/saved/SavedForms': {SavedConfirmSheet: 'SavedConfirmSheet', SavedNameSheet: 'SavedNameSheet'},
    '../../../kit/saved/SavedResourceParts': {SavedResourceMenuFacts: 'Facts', SavedResourceUndo: 'Undo', SavedResourceViewInfo: 'ViewInfo',
      viewSortLabel: () => 'Newest first'},
    '../../../screens/saved/presentation': {savedLibraryName: () => 'Movies'},
    '../../shared/library/useLibrary': {browseFailure: (value: unknown) => ({failed: value})},
    '../../shared/saved/useSavedResource': hook,
  });
  let active = {visible: true, revision: 1, intent: 'enter'}, fail = true, held: Promise<void> | undefined, retries = 0, backs = 0, changes = 0;
  const intents: any[] = [];
  let pendingIntent: any;
  let snapshot: any = {phase: 'ready', personal: {resource: {id: 'collection', kind: 'collection', name: 'Before', summary: 'Original', visibility: 'private', role: 'owner',
    revision: 1, status: 'ready', entryCount: 2, actions: ['update', 'delete', 'reorder', 'entries']},
    entries: ['first', 'second'].map(id => ({id, hidden: false, media: {id: `movie-${id}`, libraryId: 'movies', kind: 'movie', title: id}})),
    nextCursor: '', pending: false, retryPending: false, mutationError: null,
    page: {sort: 'position', direction: 'asc', filter: 'all', sorts: [], filters: [], totalCount: 2, start: 0, order: {last: 'second'}}}};
  const apply = (intent: any) => {
    const personal = snapshot.personal;
    let entries = personal.entries;
    if (intent.action === 'reorder') entries = [...entries].reverse();
    snapshot = {...snapshot, personal: {...personal, entries, retryPending: false, mutationError: null,
      resource: {...personal.resource, revision: personal.resource.revision + 1, ...(intent.action === 'update' ? intent : {})}}};
  };
  const destination = {getSnapshot: () => snapshot, subscribe: () => () => {}, select: async () => {},
    mutatePersonal: async (intent: any) => {
      assert.equal(snapshot.personal.retryPending, false, 'a new command may never replace uncertain bytes');
      intents.push(intent); pendingIntent = intent; await held;
      if (fail) {snapshot = {...snapshot, personal: {...snapshot.personal, retryPending: true, mutationError: {code: 'transport', attempt: intents.length}}}; return false;}
      apply(intent); pendingIntent = undefined; return true;
    },
    retryPersonalMutation: async () => {retries++; assert.ok(pendingIntent); await held; apply(pendingIntent); pendingIntent = undefined; return true;},
  };
  const props: any = {route: {view: 'resource', resourceId: 'collection'}, destination,
    activation: {getSnapshot: () => active, subscribe: () => () => {}}, runtime: {getSnapshot: () => ({authority}), reportError() {}, artwork() {}, ambient() {}, text: {}},
    actions: {open() {}, play() {}, menu() {}}, libraries: [], back: () => {backs++;}, changed: () => {changes++;}};
  const render = () => nodes(h.render(() => SavedResourcePage(props)));
  // The page's own buttons (Play, Shuffle, Pin, ⋯) are the title frame's actions, not drawn nodes here.
  const byId = (id: string) => {
    const drawn = render();
    const frame = drawn.find(node => node.type === 'TitleFrame');
    return drawn.find(node => node.props.testID === id) ?? frame?.props.actions?.filter((action: any) => action.testID === id)
      .map((action: any) => ({props: action}))[0];
  };
  const pageAction = (id: string) => {byId('saved-resource-menu').props.onPress(); byId('saved-resource-page-menu').props.items.find((item: any) => item.id === id).onSelect();};
  return {render, byId, pageAction, intents, retries: () => retries, backs: () => backs, changes: () => changes,
    fail: (value: boolean) => {fail = value;}, hold: (value: Promise<void> | undefined) => {held = value;},
    activate: (value: typeof active) => {active = value;}, snapshot: () => snapshot, replace: (value: any) => {snapshot = value;},
    grants: (actions: string[]) => {snapshot = {...snapshot, personal: {...snapshot.personal, resource: {...snapshot.personal.resource, actions}}};},
  };
}

test('TV collection Rename edits owner visibility and retries the original locked draft once', async () => {
  const f = await fixture(); f.pageAction('rename');
  const form = f.byId('saved-rename'); assert.equal(form.props.open, true); assert.equal(form.props.withVisibility, true);
  const draft = {name: 'Renamed', summary: 'New summary', visibility: 'server'};
  form.props.onSave(draft); form.props.onSave(draft); await tick();
  assert.equal(f.intents.length, 1, 'held OK is refused synchronously');
  let current = f.byId('saved-rename'); assert.equal(current.props.locked, true); assert.equal(current.props.action, 'action.tryAgain');
  assert.equal(current.props.initialName, draft.name); assert.equal(current.props.initialSummary, draft.summary); assert.equal(current.props.initialVisibility, 'server');
  current.props.onSave({...draft, name: 'Must not replace original bytes'}); await tick();
  assert.equal(f.intents.length, 1); assert.equal(f.retries(), 1);
  assert.deepEqual(f.intents[0], {action: 'update', ...draft});
  assert.equal(f.snapshot().personal.resource.name, draft.name); assert.equal(f.byId('saved-rename').props.open, false);
});

test('a retained Retry from one failed edit cannot retry a later command on the same page', async () => {
  const f = await fixture(); f.pageAction('rename');
  f.byId('saved-rename').props.onSave({name: 'First', summary: '', visibility: 'private'}); await tick();
  const oldRetry = f.byId('saved-resource-problem').props.action.onPress;
  oldRetry(); await tick(); assert.equal(f.retries(), 1);
  f.pageAction('rename'); f.byId('saved-rename').props.onSave({name: 'Second', summary: '', visibility: 'private'}); await tick();
  oldRetry(); await tick(); assert.equal(f.retries(), 1, 'the old rendered command is retired');
  f.byId('saved-resource-problem').props.action.onPress(); await tick();
  assert.equal(f.retries(), 2); assert.equal(f.snapshot().personal.resource.name, 'Second');
});

test('failed collection Delete stays on its page; only its acknowledged Retry navigates Back', async () => {
  const f = await fixture(); f.pageAction('delete');
  f.byId('saved-delete').props.onConfirm(); await tick();
  assert.equal(f.backs(), 0); assert.equal(f.byId('saved-delete').props.open, true);
  assert.equal(f.byId('saved-delete').props.action, 'action.tryAgain');
  f.byId('saved-delete').props.onConfirm(); await tick();
  assert.deepEqual(f.intents, [{action: 'delete'}]); assert.equal(f.retries(), 1); assert.equal(f.backs(), 1);
});

test('TV collection row opens on Open and a failed Move retries the exact original reorder', async () => {
  const f = await fixture();
  f.render().find(node => node.type === 'TitleRowWindow').props.rows.find((row: any) => row.id === 'first').onMore();
  let menu = f.byId('saved-resource-row-menu'); assert.equal(menu.props.initialFocusId, 'open');
  assert.deepEqual(menu.props.items.filter((item: any) => ['up', 'down'].includes(item.id)).map((item: any) => [item.id, item.closeOnSelect]), [['up', false], ['down', false]]);
  const down = menu.props.items.find((item: any) => item.id === 'down'); down.onSelect(); down.onSelect(); await tick();
  assert.equal(f.byId('saved-resource-row-menu').props.open, false);
  assert.deepEqual(f.intents, [{action: 'reorder', entryIds: ['first'], afterEntryId: 'second'}]);
  f.byId('saved-resource-problem').props.action.onPress(); await tick();
  assert.equal(f.retries(), 1); assert.equal(f.intents.length, 1);
  assert.deepEqual(f.snapshot().personal.entries.map((row: any) => row.id), ['second', 'first']);
});

test('a completion from before leave-and-return cannot close or navigate the new activation', async () => {
  const f = await fixture(); let release!: () => void;
  f.fail(false); f.hold(new Promise<void>(resolve => {release = resolve;})); f.pageAction('delete');
  f.byId('saved-delete').props.onConfirm(); await tick();
  f.activate({visible: false, revision: 2, intent: 'leave'}); f.render();
  f.activate({visible: true, revision: 3, intent: 'return'}); f.render();
  release(); await tick(); assert.equal(f.backs(), 0); assert.equal(f.changes(), 0);
});

test('collection page menus advertise only the current resource grants', async () => {
  const f = await fixture(); f.grants([]); f.byId('saved-resource-menu').props.onPress();
  assert.deepEqual(f.byId('saved-resource-page-menu').props.items, []);
});

test('a TV collection page starts on Play all, keeps its own ⋯ beside Shuffle and Pin, and says how to move rows', async () => {
  const f = await fixture(), drawn = f.render(), frame = drawn.find(node => node.type === 'TitleFrame');
  assert.equal(frame.props.startFocus, 'saved-play');
  assert.equal(frame.props.primary.label, 'next.cards.playAll');
  assert.deepEqual(frame.props.actions.map((action: any) => action.testID), ['saved-shuffle', 'saved-pin', 'saved-resource-menu']);
  assert.ok(drawn.some(node => node.type === 'TitleRowWindow' && node.props.longPressOnly), 'OK opens a row; holding OK opens its sheet');
  assert.ok(drawn.some(node => node.type === 'KeyHints'));
});

test('an empty TV collection starts on its first action that can be pressed, not on a Play all it cannot press', async () => {
  const f = await fixture(), before = f.snapshot();
  f.replace({...before, personal: {...before.personal, entries: [], resource: {...before.personal.resource, entryCount: 0},
    page: {...before.personal.page, totalCount: 0}}});
  let frame = f.render().find(node => node.type === 'TitleFrame');
  assert.equal(frame.props.primary.disabled, true);
  assert.equal(frame.props.startFocus, 'saved-resource-menu', 'no pin grant: the options');
  f.grants(['update', 'delete', 'reorder', 'entries', 'pin']);
  frame = f.render().find(node => node.type === 'TitleFrame');
  assert.equal(frame.props.startFocus, 'saved-pin');
});
