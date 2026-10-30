import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {NativeShellNavigation, nativeDestinationActivation} from '../src-next/shell/routes.ts';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';
import {NativeLibrarySaveView} from '../src-next/screens/library/save-view.ts';
import {nativeLibraryCanSave, nativeLibraryEntryKey} from '../src-next/screens/library/model.ts';
import * as nativeMarkAll from '../src-next/screens/library/mark-all.ts';
import * as nativeSelectionDisplay from '../src-next/shell/selection-display.ts';
import * as catalogueQuerySelection from '../../../packages/client-core/src/catalogue-query-selection.ts';
import * as selectionModel from '../../../packages/client-core/src/selection-model';
import * as mediaSelection from '../../../packages/client-core/src/media-selection.ts';
import * as browseWholeSelection from '../../../packages/client-core/src/browse-whole-selection.ts';
import * as selectionUnavailableActions from '../src-next/screens/library/selection-unavailable-actions.ts';
import * as wholeQueryUnavailable from '../src-next/screens/library/whole-query-unavailable.ts';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {createTheme} from '../src-next/kit/theme.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {browseNameId} from '../../../packages/client-core/src/presentation/browse-toolbar.ts';

test('the handheld Library module self-registers real actions and a captured receipt-backed Save-view sheet', async () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home-only'};
  const navigation = new NativeShellNavigation(authority, () => authority);
  navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: 'actual'}}, authority);
  let kind: string | undefined, create: any, ports: any, disposed = 0;
  const {react} = hooks(), posts: any[] = [], operationId = '12345678-1234-4123-8123-123456789abc';
  const theme = createTheme({width: 390, isTV: false});
  const playSelection = await screenModule(new URL('../src-next/screens/library/play-selection.ts', import.meta.url), {
    react, '../../../../../packages/client-core/src/selection-model': selectionModel, '../../../../../packages/client-core/src/catalogue-query-selection': catalogueQuerySelection,
    '../../../../../packages/client-core/src/browse-whole-selection': browseWholeSelection, '../../shell/routes': {nativeDestinationActivation},
    '../destinations/lifetime': {nativeDestinationLifetime}, '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => operationId})},
  });
  const {createNativeLibrarySelection} = await screenModule(new URL('../src-next/screens/library/selection.tsx', import.meta.url), {
    './selection-actions': {nativeSelectionActionInput: () => {throw new Error('unexpected query action');}}, '../downloads/request': {}, '../../platform/offline-downloads': {},
    react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../packages/client-core/src/catalogue-query-selection': catalogueQuerySelection, '../../../../../packages/client-core/src/selection-model': selectionModel,
    '../../../../../packages/client-core/src/media-selection': mediaSelection, '../../shell/selection-display': nativeSelectionDisplay,
    '../../kit/selection': {BulkEditor: 'BulkEditor', DeleteMediaDialog: 'DeleteMediaDialog', SelectionBar: 'SelectionBar', SelectionPhoneActionBar: 'SelectionPhoneActionBar'},
    '../../kit/theme': {useTheme: () => theme}, '../../kit/overlays': {Dialog: 'Dialog', SidePanel: 'SidePanel'}, '../../kit/controls': {Button: 'Button'},
    '../destinations/lifetime': {nativeDestinationLifetime}, '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => operationId})},
    '../destinations/DestinationPicker': {NativeDestinationPicker: 'NativeDestinationPicker'}, '../search/loaded-destinations': {NativeLoadedDestinations: 'NativeLoadedDestinations'},
    '../destinations/bulk-media': {NativeBulkMedia: 'NativeBulkMedia'}, './model': {nativeLibraryEntryKey}, './play-selection': playSelection,
    './selection-unavailable-actions': selectionUnavailableActions, './whole-query-unavailable': wholeQueryUnavailable,
  });
  const {NativeLibraryMarkAllDialog} = await screenModule(new URL('../src-next/screens/library/MarkAllDialog.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/controls': {Button: 'Button'}, '../../kit/overlays': {Banner: 'Banner', Dialog: 'Dialog'}, '../../kit/theme': {useTheme: () => theme},
  });
  const {createNativeHandheldLibraryModule} = await screenModule(new URL('../src-next/screens/library/register.tsx', import.meta.url), {
    react, '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/browse-toolbar': {browseNameId},
    '../../shell/route-registry': {nativeShellRoutes: {registerDestination: (value: string, factory: any) => {kind = value; create = factory;}}},
    '../../shell/routes': {nativeDestinationActivation}, '../destinations/lifetime': {nativeDestinationLifetime},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => operationId})},
    '../destinations/entry-actions': {createNativeEntryActions: () => ({open() {}, play() {}, menu() {}, dispose() {}})},
    './index': {createNativeLibraryRenderer: (_context: unknown, actual: unknown) => {ports = actual; return {render: () => null, dispose: () => {disposed++;}};}},
    './model': {nativeLibraryCanSave}, './save-view': {NativeLibrarySaveView}, './SaveViewSheet': {NativeLibrarySaveViewSheet: 'SaveViewSheet'},
    './selection': {createNativeLibrarySelection}, './mark-all': {NativeLibraryMarkAll: nativeMarkAll.NativeLibraryMarkAll}, './MarkAllDialog': {NativeLibraryMarkAllDialog},
  });
  assert.equal(kind, 'library'); assert.equal(create, createNativeHandheldLibraryModule);
  const context = {current: () => true, navigation, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local'}}),
    api: {request: async (path: string, method?: string, body?: unknown) => {
      if (method === 'POST') {posts.push(body); return {serverId: 'server', viewerFence: 'actual-viewer', receipt: {operationId, revision: 1, resourceId: 'new-view', deleted: false}};}
      return {serverId: 'server', viewerFence: 'actual-viewer', revision: 1, resources: [], nextCursor: ''};
    }}};
  const module = create(context);
  const definition = {libraryId: 'actual', pivot: 'movies', query: {variant: 'predicate', field: 'favorite', operator: 'equals', value: true}, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'};
  const source = {phase: 'ready', library: {id: 'actual'}, tab: {}, discover: {appended: []}, defaultSort: {field: 'title', direction: 'asc'}};
  let captured = 0;
  const destination = {getSnapshot: () => source, captureSavedView: () => {captured++; return definition;}};
  const actions = ports.actions(destination), draft = {predicates: [definition.query], sort: definition.sort[0]};
  actions.saveView(destination, draft); assert.equal(captured, 1);
  // The drawn overlays are the naming outlet then the Mark all dialog outlet.
  const [outlet] = module.renderOverlays({}).props.children, naming = outlet.props.store.getSnapshot();
  naming.setName('Actual view name'); await naming.save();
  assert.equal(posts.length, 1); assert.deepEqual(posts[0].definition, definition); assert.equal(posts[0].name, 'Actual view name');
  assert.equal(outlet.props.store.getSnapshot(), undefined, 'only a real saved receipt closes the owned naming outlet');
  actions.saveView(destination, draft);
  const retired = outlet.props.store.getSnapshot(); navigation.open({kind: 'shell', destination: {kind: 'home'}}, authority);
  assert.equal(outlet.props.store.getSnapshot(), undefined); assert.equal(retired.getSnapshot().open, false);
  module.dispose(); assert.equal(disposed, 1);
});
