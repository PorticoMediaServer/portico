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
  const {SavedResourceScreen} = await screenModule(new URL('../src-next/screens/saved/SavedResourceScreen.tsx', import.meta.url), {'./paging': pagingModule, './SavedReorderList': {SavedReorderList: 'SavedReorderList'}, '../../../../../packages/client-core/src/saved': savedCore, './SavedSortMenu': {SavedSortMenu: 'SavedSortMenu'}, './SavedLoading': {SavedLoading: 'SavedLoading', SavedTail: 'SavedTail'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, 
    react, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, has: () => true}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Retry this saved change'})},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Switch: 'Switch'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'AmbientArt', Artwork: 'Artwork', MediaArt: 'MediaArt'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame', Parts: 'Parts'}, '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard'},
    '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet', Menu: 'Menu', ToastHost: 'ToastHost'},
    '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {StateView: 'StateView'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/browse/presentation': {alpha: () => '', browseType: () => ({})},
    '../../kit/theme': {useTheme: () => theme}, '../../shell/model': {sameAuthority: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)},
    '../destinations/focus': {nativeDestinationFocusIds: () => ({rail: 'rail'})}, '../home/model': {homeCardText: () => ({})},
    './SavedForms': {SavedConfirmSheet: 'SavedConfirmSheet', SavedNameSheet: 'SavedNameSheet'}, '../read/platform': {isReadingLibraryKind: () => false},
    './presentation': {exactDurationSeconds: () => undefined, personalMosaic: () => [], playlistEntryMosaic: () => [], savedDuration: String,
      savedItemCount: String, savedLibraryName: () => 'Movies', savedPlaylistEyebrow: 'Playlist', savedResourceEyebrow: () => 'Collection',
      savedTitleCount: String, savedViewConditionCount: () => 0, savedVisibility: () => ({text: 'Private'}), savedVisibilityHelp: () => ''},
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
  const render = () => nodes(h.render(() => SavedResourceScreen(props)));
  const byId = (id: string) => render().find(node => node.props.testID === id);
  const pageAction = (id: string) => {byId('saved-resource-menu').props.onPress(); byId('saved-resource-page-menu').props.items.find((item: any) => item.id === id).onSelect();};
  return {render, byId, pageAction, intents, retries: () => retries, backs: () => backs, changes: () => changes,
    fail: (value: boolean) => {fail = value;}, hold: (value: Promise<void> | undefined) => {held = value;},
    activate: (value: typeof active) => {active = value;}, snapshot: () => snapshot,
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
  f.render().find(node => node.props.row?.id === 'first').props.onMore();
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
