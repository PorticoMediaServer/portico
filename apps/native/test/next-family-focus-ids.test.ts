// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {registerHooks} from 'node:module';
import React, {type ReactElement, type ReactNode} from 'react';
import {transformSync} from 'rolldown/utils';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {createTheme, fontFamilies, nativeType} from '../src-next/kit/theme.ts';
import type {DockOccupant} from '../src-next/kit/dock/types.ts';
import type {QueuePlaybackFence, QueueScope} from '../../../packages/player-core/src/index.ts';
import {playbackIdentity} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

// Exercise the actual family, controls and overlay composition. Only native
// hosts, animation/gesture drivers and the theme context are replaced. This
// element/hook harness cannot establish native layout or physical D-pad focus.
const state = {theme: createTheme({isTV: true, width: 1920}), fontFamilies, nativeType};
const stateKey = '__porticoFamilyFocusIDsTest';
Object.assign(globalThis, {[stateKey]: state});
const moduleURL = (source: string) => `data:text/javascript,${encodeURIComponent(source)}`;
const nativeStub = new URL('./react-native-stub.mjs', import.meta.url).href;
const native = moduleURL(`export * from ${JSON.stringify(nativeStub)};
  export const Platform={OS:'android',isTV:true};
  export const View='View',Text='Text',TextInput='TextInput',Pressable='Pressable',Modal='Modal',ScrollView='ScrollView',FlatList='FlatList',TVFocusGuideView='TVFocusGuideView',ActivityIndicator='ActivityIndicator';
  export const Image='Image',PixelRatio={get:()=>1},Animated={View:'AnimatedView'};
  export const BackHandler={addEventListener:()=>({remove(){}})};
  // Android family fixtures never install the Apple Menu recognizer.
  export const TVEventControl={enableTVMenuKey(){throw Error('unexpected_apple_tv_menu_capture');},disableTVMenuKey(){throw Error('unexpected_apple_tv_menu_release');}};
  export const useWindowDimensions=()=>({width:1920,height:1080,scale:1,fontScale:1});
  const flatten=s=>Array.isArray(s)?Object.assign({},...s.map(flatten)):s??{};
  export const StyleSheet={create:s=>s,flatten,absoluteFill:{position:'absolute',inset:0}};`);
const theme = moduleURL(`const s=globalThis.${stateKey}; export const activeTextScale=()=>s.theme.textScale; export const useTheme=()=>s.theme; export const fontFamilies=s.fontFamilies; export const nativeType=s.nativeType;`);
const reanimated = moduleURL(`import {useRef} from ${JSON.stringify(import.meta.resolve('react'))};
  export const useSharedValue=value=>useRef({value}).current;
  export const useAnimatedStyle=fn=>fn(); export const cancelAnimation=()=>{};
  export const withTiming=value=>value,withSpring=value=>value,runOnJS=fn=>fn;
  export const Easing={bezier:()=>v=>v,linear:v=>v},ReduceMotion={Never:'never'};
  export default {View:'AnimatedView',createAnimatedComponent:c=>c};`);
const gesture = moduleURL(`const pan=()=>new Proxy({}, {get:()=>()=>pan()}); export const Gesture={Pan:pan}; export const GestureDetector='GestureDetector';`);
const svg = moduleURL(`export default 'Svg'; export const Defs='Defs',LinearGradient='LinearGradient',RadialGradient='RadialGradient',Rect='Rect',Stop='Stop',Path='Path',Circle='Circle',Ellipse='Ellipse',Line='Line',Polyline='Polyline',Polygon='Polygon',G='G',ClipPath='ClipPath',Mask='Mask',Use='Use',Text='SvgText';`);
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '@react-native-masked-view/masked-view') return {url: moduleURL(`export default 'MaskedView';`), shortCircuit: true};
    if (specifier === 'react-native') return {url: native, shortCircuit: true};
    if (specifier === 'react-native-reanimated') return {url: reanimated, shortCircuit: true};
    if (specifier === 'react-native-gesture-handler') return {url: gesture, shortCircuit: true};
    if (specifier === 'react-native-svg') return {url: svg, shortCircuit: true};
    if (specifier === '@react-native-community/blur') return {url: moduleURL(`export const BlurView='BlurView';`), shortCircuit: true};
    if (specifier === 'react-native-safe-area-context') return {url: moduleURL(`export const useSafeAreaInsets=()=>({top:0,bottom:0,left:0,right:0});`), shortCircuit: true};
    if (specifier.startsWith('.') && context.parentURL?.startsWith('file:') && /\/kit\/theme(?:\.ts)?$/.test(new URL(specifier, context.parentURL).pathname)) return {url: theme, shortCircuit: true};
    try {return next(specifier, context);} catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ERR_MODULE_NOT_FOUND' || !specifier.startsWith('.')) throw error;
      for (const suffix of ['.ts', '.tsx', '/index.ts', '/index.tsx']) {
        try {return next(specifier + suffix, context);} catch (retry) {if ((retry as NodeJS.ErrnoException).code !== 'ERR_MODULE_NOT_FOUND') throw retry;}
      }
      throw error;
    }
  },
  load(url, context, next) {
    if (!url.endsWith('.tsx')) return next(url, context);
    const filename = fileURLToPath(url);
    const result = transformSync(filename, readFileSync(filename, 'utf8'), {lang: 'tsx', jsx: {runtime: 'automatic'}, target: 'es2022'});
    if (result.errors.length) throw new AggregateError(result.errors, `Cannot transform ${filename}`);
    return {format: 'module', source: result.code, shortCircuit: true};
  },
});

const {focusTestID} = await import('../src-next/kit/focus/registry.ts');
const {FocusRegion} = await import('../src-next/kit/focus/FocusRegion.tsx');
const {Button} = await import('../src-next/kit/controls/Button.tsx');
const {Focusable} = await import('../src-next/kit/controls/Focusable.tsx');
const {Select} = await import('../src-next/kit/controls/Select.tsx');
const {Menu} = await import('../src-next/kit/overlays/Menu.tsx');
const {StateView} = await import('../src-next/kit/states/StateView.tsx');
const {StateActionButton} = await import('../src-next/kit/states/StateActionButton.tsx');
const {ProfilePicker} = await import('../src-next/kit/artwork/Profiles.tsx');
const {SettingsRow, ChoiceMatrix, LanguageList} = await import('../src-next/kit/shell/SettingsControls.tsx');
const {PersonRow, DeviceRow} = await import('../src-next/kit/shell/Diagnostics.tsx');
const {NowPlayingCard} = await import('../src-next/kit/dock/NowPlayingCard.tsx');
const {AlphabetRail} = await import('../src-next/kit/layout/AlphabetRail.tsx');
const id = (prefix: string, ...parts: readonly string[]) => focusTestID(prefix, JSON.stringify(parts));
type Element = ReactElement<Record<string, any>>;
function elements(node: ReactNode): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!React.isValidElement<Record<string, any>>(node)) return [];
  return [node, ...elements(node.props.children)];
}
const ofType = (node: ReactNode, type: unknown) => elements(node).filter(element => element.type === type);
async function mount(component: any, props: any) {
  let tree: ReactNode;
  const host = createFabricHookHost(() => {tree = typeof component === 'function' ? component(props) : component.render(props, props.ref ?? null);});
  await host.mount();
  return {get tree() {return tree;}, async update(next: any) {props = next; await host.rerender();}, host};
}
async function nativeControlID(element: Element): Promise<string | undefined> {
  const mounted: Awaited<ReturnType<typeof mount>>[] = [];
  try {
    let current = element;
    while (typeof current.type !== 'string') {
      const instance = await mount(current.type, current.props); mounted.push(instance);
      current = instance.tree as Element;
    }
    assert.equal(current.type, 'Pressable');
    return current.props.testID;
  } finally {for (const instance of mounted.reverse()) await instance.host.unmount();}
}


test('state actions derive semantic host IDs and preserve explicit native IDs, callbacks and refs', async () => {
  let pressed = 0;
  const ref = {current: null};
  const view = await mount(StateView, {testID: 'tv.authority.error', title: 'Error', primaryAction: {id: 'retry', label: 'Retry', onPress: () => {pressed++;}}, secondaryAction: {id: 'back', label: 'Back', testID: 'tv.authority.explicit-back', ref, onPress() {}}});
  try {
    const actions = ofType(view.tree, StateActionButton);
    const primary = await mount(StateActionButton, actions[0]!.props), secondary = await mount(StateActionButton, actions[1]!.props);
    try {
      assert.equal(await nativeControlID(primary.tree as Element), id('tv.authority.error', 'action', 'retry'));
      assert.equal(await nativeControlID(secondary.tree as Element), 'tv.authority.explicit-back');
      assert.equal((secondary.tree as Element).props.ref, ref);
      assert.equal((primary.tree as Element).props.id, undefined);
      (primary.tree as Element).props.onPress(); assert.equal(pressed, 1);
    } finally {await primary.host.unmount(); await secondary.host.unmount();}
  } finally {await view.host.unmount();}
});

test('profile hosts keep entity identity through reorder and honor explicit caller overrides', async () => {
  const selected: string[] = [];
  const profiles = [{id: 'one', name: 'Same', initial: 'A'}, {id: 'two', name: 'Same', initial: 'B', unavailableReason: 'restricted'}];
  const props = {testID: 'dev.artwork.profiles', profiles, onSelect: (key: string) => selected.push(key), onAdd() {}, onManage() {}, profileProps: (profile: {id: string}) => profile.id === 'two' ? {testID: 'dev.artwork.explicit-two'} : {}};
  const picker = await mount(ProfilePicker, props);
  try {
    const rows = ofType(picker.tree, Focusable);
    const first = await Promise.all(rows.map(nativeControlID));
    assert.deepEqual(first, [id(props.testID, 'profile', 'one'), 'dev.artwork.explicit-two', id(props.testID, 'add')]);
    rows[0]!.props.onPress(); assert.deepEqual(selected, ['one']);
    assert.equal(rows[1]!.props.disabled, true);
    assert.equal(await nativeControlID(ofType(picker.tree, Button)[0]!), id(props.testID, 'manage'));
    await picker.update({...props, profiles: [...profiles].reverse().map(profile => ({...profile, name: 'Translated'}))});
    assert.deepEqual(await Promise.all(ofType(picker.tree, Focusable).slice(0, 2).map(nativeControlID)), first.slice(0, 2).reverse());
  } finally {await picker.host.unmount();}
});

test('settings slots retain a sole native target, entity rows and language menus use caller fences', async () => {
  const slot = React.createElement(Select, {testID: 'tv.settings.autoplay', options: [], value: ''});
  const row = await mount(SettingsRow, {testID: 'tv.settings.row', label: 'Autoplay', readable: true, control: slot});
  try {assert.equal(ofType(row.tree, Focusable).length, 0); assert.equal(ofType(row.tree, Select)[0], slot);} finally {await row.host.unmount();}
  const matrix = await mount(ChoiceMatrix, {testID: 'tv.settings.skip', rows: [{id: 'intro', label: 'Intro', value: 'ask'}], options: [{value: 'ask', label: 'Ask'}], onChange() {}});
  try {assert.equal(ofType(matrix.tree, SettingsRow)[0]!.props.control.props.testID, id('tv.settings.skip', 'row', 'intro'));} finally {await matrix.host.unmount();}
  for (const [component, props] of [[PersonRow, {id: 'person-one', name: 'Same', role: 'member', face: {initial: 'A'}, facts: []}], [DeviceRow, {id: 'device-one', name: 'Same', icon: 'i-tv', facts: []}]] as const) {
    const entity = await mount(component, {...props, testID: 'tv.settings.entities'});
    try {assert.equal(await nativeControlID(ofType(entity.tree, Focusable)[0]!), id('tv.settings.entities', 'entity', props.id));} finally {await entity.host.unmount();}
  }
  const languages = await mount(LanguageList, {testID: 'tv.settings.languages', label: 'Languages', value: [{id: 'en', label: 'English'}], available: [{id: 'fr', label: 'French'}], limit: 4, onChange() {}});
  try {
    const rows = ofType(languages.tree, Focusable), host = {requestTVFocus() {}};
    rows[0]!.props.ref(host); rows[0]!.props.onPress(); await languages.host.flush();
    const menu = ofType(languages.tree, Menu)[0]!;
    assert.equal(menu.props.testID, id('tv.settings.languages', 'language-menu', 'en'));
    assert.equal(menu.props.returnFocusRef.current, host);
    assert.equal(await nativeControlID(rows[0]!), id('tv.settings.languages', 'language', 'en'));
    assert.equal(ofType(languages.tree, Menu)[1]!.props.testID, id('tv.settings.languages', 'add-menu'));
  } finally {await languages.host.unmount();}
});

const scope: QueueScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', sourceId: 'source', itemId: 'song', entryId: 'entry', generation: 1};
const occupant: DockOccupant = {mode: 'music', playback: {fence, kind: 'music', queue: null, positionSeconds: 75, playing: true, expanded: false}, presentation: {fence, identity: playbackIdentity({kind: 'music', item: {id: 'song', kind: 'song', title: 'Song', artist: 'Artist'}}, 'dock', {t: defaultI18n.t}), durationSeconds: 100, skipBackSeconds: 10, capabilities: {play: true, pause: true, seek: true, stop: true, previous: false, next: true, cast: false}}};

test('TV dock holds the shared region, keeps transport identity on pause/play and resolves the remounted opener', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const originalRAF = globalThis.requestAnimationFrame, originalCancel = globalThis.cancelAnimationFrame;
  let restore: (() => void) | undefined;
  globalThis.requestAnimationFrame = callback => {restore = () => callback(0); return 1;};
  globalThis.cancelAnimationFrame = () => {restore = undefined;};
  const calls: unknown[] = [], prefix = 'tv.authority.playback.session';
  const props = {occupant, testID: prefix, expanded: false, onExpandedChange: (expanded: boolean) => calls.push(expanded), onAction: (command: unknown) => calls.push(command)};
  const dock = await mount(NowPlayingCard, props);
  try {
    assert.equal(await nativeControlID(ofType(dock.tree, Focusable)[0]!), id(prefix, 'open'));
    await dock.update({...props, expanded: true});
    const region = ofType(dock.tree, FocusRegion)[0]!;
    assert.equal(region.props.hold, true); assert.equal(region.props.id, prefix); assert.equal(region.props.entry, id(prefix, 'transport'));
    const transport = ofType(dock.tree, Focusable).find(element => element.props.testID === id(prefix, 'transport'))!;
    const hostID = await nativeControlID(transport); transport.props.onPress(); assert.equal((calls.at(-1) as {type: string}).type, 'pause');
    await dock.update({...props, expanded: true, occupant: {...occupant, playback: {...occupant.playback, playing: false}}});
    const play = ofType(dock.tree, Focusable).find(element => element.props.testID === hostID)!;
    assert.equal(await nativeControlID(play), hostID); play.props.onPress(); assert.equal((calls.at(-1) as {type: string}).type, 'play');
    await dock.update({...props, expanded: false});
    const quiet = ofType(dock.tree, Focusable)[0]!;
    const focus = await mount(Focusable, quiet.props), adapter = await mount((focus.tree as Element).type, (focus.tree as Element).props);
    try {(adapter.tree as Element).props.ref({requestTVFocus() {calls.push('remounted-opener'); (adapter.tree as Element).props.onFocus({});}}); restore?.(); assert.equal(calls.at(-1), 'remounted-opener', 'restoration does not immediately re-expand the quiet dock');
      quiet.props.onPress(); assert.equal(calls.at(-1), true, 'explicit OK still expands the restored opener');} finally {await adapter.host.unmount(); await focus.host.unmount();}
  } finally {await dock.host.unmount(); globalThis.requestAnimationFrame = originalRAF; globalThis.cancelAnimationFrame = originalCancel;}
});

test('alphabet host IDs use bucket keys, keep empty buckets disabled and deliver actual jump coordinates', async () => {
  const calls: unknown[] = [];
  const rail = await mount(AlphabetRail, {testID: 'tv.library.alphabet', positionIndex: {A: 3, B: 10}, total: 20, firstVisible: 3, lastVisible: 6, onJump: (...args: unknown[]) => calls.push(args)});
  try {
    const rows = ofType(rail.tree, Focusable);
    const a = rows.find(row => row.props.testID === id('tv.library.alphabet', 'bucket', 'A'))!;
    assert.equal(await nativeControlID(a), id('tv.library.alphabet', 'bucket', 'A')); a.props.onPress(); assert.deepEqual(calls, [[3, 'A']]);
    assert.equal(rows.find(row => row.props.testID === id('tv.library.alphabet', 'bucket', 'C'))!.props.disabled, true);
  } finally {await rail.host.unmount();}
});

// FX095 authored UNRUN for hourly CI. Element declarations do not qualify native focus or Home landing.
test('scoped TV profile entry uses actual current/remembered offered face and keeps busy native leaves', async () => {
  const previousTheme = state.theme; state.theme = createTheme({isTV: true, width: 1920});
  const profiles = [{id: 'first', name: 'First', initial: 'F'}, {id: 'current', name: 'Current', initial: 'C'},
    {id: 'blocked', name: 'Blocked', initial: 'B', unavailableReason: 'restricted'}];
  let calls = 0;
  const props = {focusScope: 'actual-profile-opening', selectedId: 'current', profiles, onSelect() {},
    profileProps: () => {calls++; return {};}};
  const picker = await mount(ProfilePicker, props);
  try {
    const region = picker.tree as Element;
    assert.equal(region.props.entry, 'current'); assert.equal(calls, 3, 'caller props are evaluated once per actual face');
    assert.deepEqual(region.props.order, ['first', 'current']);
    let faces = ofType(picker.tree, Focusable);
    assert.ok(faces.every(face => face.props.hasTVPreferredFocus === false), 'one shared lifecycle request owns entry');
    assert.equal(faces[1]!.props.focusId, 'current');
    await picker.update({...props, preferredProfileId: 'first'});
    assert.equal((picker.tree as Element).props.entry, 'first', 'original focused PIN opener has precedence');
    await picker.update({...props, preferredProfileId: 'blocked'});
    assert.equal((picker.tree as Element).props.entry, 'current', 'unavailable is never an invented entry');
    await picker.update({...props, focusBusy: true, profiles: profiles.map(profile => ({...profile, disabled: true}))});
    faces = ofType(picker.tree, Focusable);
    assert.equal(faces[1]!.props.disabled, true); assert.equal(faces[1]!.props.focusableWhenDisabled, true);
    assert.equal(faces[2]!.props.focusableWhenDisabled, false);
    const content = faces[1]!.props.children({focused: true});
    assert.equal(content.props.retainDisabledFocus, true, 'same focused face retains its visual ring while checking');
  } finally {await picker.host.unmount(); state.theme = previousTheme;}
});

// FX-173 / BLK-TV-006 authored UNRUN source: the shared state action must reach its
// FocusRegion with an identity. Element declarations here prove the identity the
// region consumes; they do not claim native graph acceptance or a device walk.
test('shared state actions hand their effective identity to the focus region, including the no-prefix fallback', async () => {
  let pressed = 0;
  const noPrefix = await mount(StateActionButton, {action: {id: 'retry', label: 'Retry', onPress: () => {pressed++;}}});
  try {
    const button = noPrefix.tree as Element;
    // Without any caller testID the caller's required semantic action id is the identity.
    assert.equal((button.props as any).focusId, 'retry');
    assert.equal((button.props as any).testID, undefined);
    assert.equal(await nativeControlID(button), undefined, 'no testID is invented for the native host');
    (button.props as any).onPress(); assert.equal(pressed, 1);
  } finally {await noPrefix.host.unmount();}

  const prefixed = await mount(StateActionButton, {testID: 'native-start-refusal', action: {id: 'retry', label: 'Retry', onPress() {}}});
  try {
    const button = prefixed.tree as Element, expected = id('native-start-refusal', 'action', 'retry');
    assert.equal((button.props as any).testID, expected, 'the derived testID is unchanged');
    assert.equal((button.props as any).focusId, expected, 'the region receives that same identity as focusId');
    assert.equal(await nativeControlID(button), expected);
  } finally {await prefixed.host.unmount();}

  const overridden = await mount(StateActionButton, {testID: 'native-start-refusal', action: {id: 'retry', label: 'Retry', testID: 'native-start-explicit', onPress() {}}});
  try {
    const button = overridden.tree as Element;
    assert.equal((button.props as any).testID, 'native-start-explicit', 'an action testID override still wins');
    assert.equal((button.props as any).focusId, 'native-start-explicit');
    assert.equal(await nativeControlID(button), 'native-start-explicit');
  } finally {await overridden.host.unmount();}
});

test('a state action keeps one identity across busy, enabled and sibling compositions', async () => {
  const action = {id: 'retry', label: 'Retry', onPress() {}};
  // One mounted binding across busy -> enabled: the identity and the eligibility of
  // the same child are what a region retains, not a replacement element.
  const binding = await mount(StateActionButton, {testID: 'tv.people.failure', action, loading: true, disabled: true});
  try {
    assert.equal((binding.tree as Element).props.focusId, id('tv.people.failure', 'action', 'retry'));
    assert.equal(await nativeControlID(binding.tree as Element), id('tv.people.failure', 'action', 'retry'));
    await binding.update({testID: 'tv.people.failure', action, loading: false, disabled: false});
    assert.equal((binding.tree as Element).props.focusId, id('tv.people.failure', 'action', 'retry'), 'busy to enabled keeps the same identity');
    assert.equal(await nativeControlID(binding.tree as Element), id('tv.people.failure', 'action', 'retry'), 'the same native child keeps that testID');
    assert.equal((binding.tree as Element).props.disabled, false);
  } finally {await binding.host.unmount();}

  // TVPeoplePanel's composition: one FocusRegion, a runtime error StateView whose
  // actions are the shared component, and distinct sibling prefixes elsewhere.
  const composition = await mount(StateView, {title: 'Error', primaryAction: {id: 'retry', label: 'Retry', onPress() {}}, secondaryAction: {id: 'back', label: 'Back', testID: 'tv.people.back', onPress() {}}, additionalActions: [{id: 'details', label: 'Details', onPress() {}}]});
  try {
    const actions = ofType(composition.tree, StateActionButton);
    assert.equal(actions.length, 3);
    const mounted = await Promise.all(actions.map(element => mount(StateActionButton, element.props)));
    try {
      const ids = mounted.map(instance => (instance.tree as Element).props.focusId);
      // No StateView prefix here: each action's own semantic id is its identity.
      assert.deepEqual(ids, ['retry', 'tv.people.back', 'details']);
      assert.equal(new Set(ids).size, 3, 'primary, secondary and additional actions never collide');
    } finally {for (const instance of mounted) await instance.host.unmount();}
  } finally {await composition.host.unmount();}

  const library = await mount(StateView, {testID: 'tv.library.failure', title: 'Error', primaryAction: {id: 'retry', label: 'Retry', onPress() {}}});
  try {
    const libraryAction = await mount(StateActionButton, ofType(library.tree, StateActionButton)[0]!.props);
    try {assert.equal((libraryAction.tree as Element).props.focusId, id('tv.library.failure', 'action', 'retry'), 'distinct sibling prefixes keep distinct identities');}
    finally {await libraryAction.host.unmount();}
  } finally {await library.host.unmount();}
});
