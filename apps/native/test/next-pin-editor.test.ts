import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {NativeDestinationPins} from '../src-next/screens/destinations/pins.ts';
import {nativePinDrop} from '../src-next/screens/destinations/pins-drag.ts';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

function nodes(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  return node && typeof node === 'object' && 'props' in node ? [node, ...nodes(node.props.children)] : [];
}

test('ruling39 pin rows keep immediate toggles and handle accessibility moves without a Save/Reset or arrow footer', async () => {
  const host = hooks(), theme = createTheme({width: 390, isTV: false}), calls: unknown[] = [];
  const state = {phase: 'ready', busy: false, libraries: {revision: 3, pinnedLibraryIds: ['b', 'a']}, resources: {order: []}};
  // The drawn row motion boundary: shared values and gesture factories are
  // controlled UI seams. The pure drop model and every pin write stay real.
  const motion = {default: {View: 'Animated.View'}, useSharedValue: (value: unknown) => ({value}),
    useAnimatedStyle: (read: () => unknown) => ({read}), runOnJS: (fn: (...args: unknown[]) => void) => fn};
  const {usePinDragPaint} = await screenModule(new URL('../src-next/screens/destinations/pins-drag-paint.ts', import.meta.url), {
    react: host.react, 'react-native-reanimated': motion,
  });
  const pan = () => {const builder: any = new Proxy({}, {get: () => (..._args: unknown[]) => builder}); return builder;};
  const {NativePinEditorSheet} = await screenModule(new URL('../src-next/screens/destinations/PinEditorSheet.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-reanimated': motion,
    'react-native-gesture-handler': {Gesture: {Pan: pan}, GestureDetector: 'GestureDetector'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/errors': {},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet'},
    '../../kit/browse/presentation': {browseType: () => ({}), alpha: (color: string) => color}, '../../kit/theme': {useTheme: () => theme},
    './pins-drag': {nativePinDrop}, './pins-drag-paint': {usePinDragPaint},
  });
  const controller = {subscribe() {}, getSnapshot: () => state, pinLibrary: (...value: unknown[]) => calls.push(value), placeLibrary() {}, moveLibrary() {}};
  const tree = host.render(() => NativePinEditorSheet({controller, libraries: [{id: 'a', name: 'Actual A'}, {id: 'b', name: 'Actual B'}, {id: 'c', name: 'Actual C'}], onClose() {}}));
  assert.equal(tree.type, 'BottomSheet'); assert.equal(tree.props.title, defaultI18n.t('pins.editTitle'));
  assert.equal(tree.props.description, defaultI18n.t('pins.librariesHint')); assert.equal(tree.props.footer, undefined);
  const group = nodes(tree).find(node => node.type?.name === 'PinGroup');
  assert.deepEqual(group.props.rows.map((row: any) => [row.id, row.pinned, row.moveable]), [['b', true, true], ['a', true, true], ['c', false, false]]);
  const rows = host.render(() => group.type(group.props)), controls = nodes(rows).filter(node => node.type === 'Focusable');
  assert.equal(controls.length, 3); assert.ok(controls.every(node => node.props.accessibilityRole === 'togglebutton'));
  assert.deepEqual(controls.map(node => node.props.accessibilityState.checked), [true, true, false]);
  controls[0].props.onPress(); assert.deepEqual(calls, [['b', false]]);
  const handles = nodes(rows).filter(node => node.type?.name === 'PinHandle'); assert.equal(handles.length, 2);
  assert.equal(handles[0].props.first, true); assert.equal(handles[0].props.last, false);
  assert.equal(handles[1].props.first, false); assert.equal(handles[1].props.last, true);
  const ids = controls.map(node => node.props.focusId); assert.equal(new Set(ids).size, ids.length);
});

test('drag insertion uses measured unequal native rows and never fabricates a position from an index', () => {
  const rows = [{id: 'a', y: 7, height: 42}, {id: 'b', y: 58, height: 70}, {id: 'c', y: 140, height: 54}];
  assert.deepEqual(nativePinDrop(rows, 'a', 110), {beforeId: 'c', lineY: 140});
  assert.deepEqual(nativePinDrop(rows, 'c', -150), {beforeId: 'a', lineY: 7});
  assert.deepEqual(nativePinDrop(rows, 'a', 300), {lineY: 194});
  assert.equal(nativePinDrop(rows, 'missing', 50), undefined);
  assert.equal(nativePinDrop(rows, 'a', Number.NaN), undefined);
});

test('all pin entries use one real route factory and Back retires its captured controller', async () => {
  const host = hooks(), registrations = new Map<string, any>(), authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home'};
  const navigation = new NativeShellNavigation(authority, () => authority);
  const {createNativeHandheldPinsModule} = await screenModule(new URL('../src-next/screens/destinations/PinsRoute.tsx', import.meta.url), {
    react: host.react, '../../shell/route-registry': {nativeShellRoutes: {registerRoute: (kind: string, factory: any) => registrations.set(kind, factory)}},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => 'actual-operation'})}, './lifetime': {nativeDestinationLifetime},
    './pins': {NativeDestinationPins}, './PinEditorSheet': {NativePinEditorSheet: 'PinEditor'},
  });
  assert.equal(registrations.get('edit-pins'), createNativeHandheldPinsModule);
  assert.equal(registrations.get('library-edit-pins'), createNativeHandheldPinsModule);
  const libraries = [{id: 'actual', name: 'Actual', kind: 'movie'}], calls: string[] = [];
  const context = {current: () => true, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local'}}), navigation, libraries: () => libraries,
    api: {request: async (path: string) => {calls.push(path); return path.endsWith('library-navigation') ? {revision: 1, pinnedLibraryIds: ['actual']}
      : {serverId: 'server', viewerFence: 'actual-viewer', revision: 1, order: []};}}};
  const module = createNativeHandheldPinsModule(context);
  navigation.open({kind: 'library-edit-pins', libraryId: 'actual'}, authority);
  const view = module.render(navigation.getSnapshot().route), controller = view.props.controller;
  await controller.load(); assert.equal(controller.getSnapshot().phase, 'ready');
  view.props.onClose(); const retired = controller.getSnapshot(); assert.equal(retired.libraries, undefined);
  await controller.pinLibrary('actual', false); assert.equal(calls.length, 2);
  navigation.open({kind: 'edit-pins'}, authority);
  assert.notEqual(module.render(navigation.getSnapshot().route).props.controller, controller);
  module.dispose(); assert.equal(module.render(navigation.getSnapshot().route), null);
});
