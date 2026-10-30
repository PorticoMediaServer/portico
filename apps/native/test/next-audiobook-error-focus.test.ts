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
const state = {theme: createTheme({isTV: true, width: 1920}), fontFamilies, nativeType, menuKeyEnabled: false};
const stateKey = '__porticoFamilyFocusIDsTest';
Object.assign(globalThis, {[stateKey]: state});
const moduleURL = (source: string) => `data:text/javascript,${encodeURIComponent(source)}`;
const nativeStub = new URL('./react-native-stub.mjs', import.meta.url).href;
const native = moduleURL(`export * from ${JSON.stringify(nativeStub)};
  export const Platform={OS:'android',isTV:true};
  export const View='View',Text='Text',TextInput='TextInput',Pressable='Pressable',Modal='Modal',ScrollView='ScrollView',FlatList='FlatList',TVFocusGuideView='TVFocusGuideView',ActivityIndicator='ActivityIndicator';
  export const Image='Image',PixelRatio={get:()=>1},Animated={View:'AnimatedView'};
  export const Easing={bezier:()=>v=>v,linear:v=>v,out:v=>v,in:v=>v,inOut:v=>v};
  export const BackHandler={addEventListener:()=>({remove(){}})};
  export const TVEventControl={enableTVMenuKey(){globalThis.__porticoFamilyFocusIDsTest.menuKeyEnabled=true;},disableTVMenuKey(){globalThis.__porticoFamilyFocusIDsTest.menuKeyEnabled=false;}};
  export const useWindowDimensions=()=>({width:1920,height:1080,scale:1,fontScale:1});
  const flatten=s=>Array.isArray(s)?Object.assign({},...s.map(flatten)):s??{};
  export const StyleSheet={create:s=>s,flatten,absoluteFill:{position:'absolute',inset:0}};`);
const theme = moduleURL(`const s=globalThis.${stateKey}; export const activeTextScale=()=>s.theme.textScale; export const useTheme=()=>s.theme; export const fontFamilies=s.fontFamilies; export const nativeType=s.nativeType;`);
const reanimated = moduleURL(`import {useRef} from ${JSON.stringify(import.meta.resolve('react'))};
  export const useSharedValue=value=>useRef({value}).current,useAnimatedStyle=fn=>fn(),cancelAnimation=()=>{},withTiming=value=>value,withSpring=value=>value,withDelay=(_,value)=>value,runOnJS=fn=>fn,runOnUI=fn=>fn,useAnimatedRef=()=>useRef(null),useAnimatedScrollHandler=()=>{},useAnimatedReaction=()=>{},scrollTo=()=>{};
  export const Easing={bezier:()=>v=>v,linear:v=>v},ReduceMotion={Never:'never'};
  export default {View:'AnimatedView',ScrollView:'AnimatedScrollView',createAnimatedComponent:c=>c};`);
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



const {AuthorSeriesScreen} = await import('../src-next/screens/destinations/audiobooks/AuthorSeriesScreen.tsx');
const {AudiobookErrorState} = await import('../src-next/screens/destinations/audiobooks/BookScreen.tsx');

test('author and series failures expose actual state retry and TV focus identity', async () => {
  for (const kind of ['author', 'book_series']) for (const tv of [false, true]) {
    state.theme = createTheme({isTV: tv, width: tv ? 1920 : 390});
    let retries = 0, backs = 0;
    const entry = {id: 'author-1', kind, libraryId: 'books', title: 'Author'};
    const controller = {getSnapshot: (() => {const snapshot = {phase: 'error', error: {code: 'request_failed'}, sections: [], finished: false}; return () => snapshot;})(),
      subscribe: () => () => {}, load: async () => {retries++;}};
    const screen = await mount(AuthorSeriesScreen, {controller, entry, runtime: {artwork: () => undefined, ambient: () => undefined},
      actions: {menu() {}}, openEntry() {}, onBack: () => {backs++;}});
    try {
      const errorElement = ofType(screen.tree, AudiobookErrorState)[0]!;
      assert.ok(errorElement);
      if (tv) assert.equal(ofType(screen.tree, FocusRegion)[0]!.props.entry, 'audiobook-author-1.retry');
      else {
        const back = ofType(screen.tree, Button).find(button => button.props.accessibilityLabel === defaultI18n.t('action.back'))!;
        back.props.onPress(); assert.equal(backs, 1);
      }
      const error = await mount(AudiobookErrorState, errorElement.props);
      try {
        const stateView = await mount(StateView, (error.tree as Element).props);
        try {
          const action = await mount(StateActionButton, ofType(stateView.tree, StateActionButton)[0]!.props);
          try {
            assert.equal(await nativeControlID(action.tree as Element), 'audiobook-author-1.retry');
            (action.tree as Element).props.onPress(); assert.equal(retries, 1);
          } finally {await action.host.unmount();}
        } finally {await stateView.host.unmount();}
      } finally {await error.host.unmount();}
    } finally {await screen.host.unmount();}
  }
});

test('actual author and series retry controls attach inside their real region and recover', async () => {
  const {FocusProvider} = await import('../src-next/kit/focus/FocusRegion.tsx');
  async function mountWithContexts(component: any, props: any, contexts: [any, unknown][]) {
    let tree: ReactNode;
    const host = createFabricHookHost(() => {
      const previous = contexts.map(([context, value]) => {const prior = context._currentValue2; context._currentValue2 = value; return [context, prior];});
      try {tree = typeof component === 'function' ? component(props) : component.render(props, props.ref ?? null);}
      finally {for (const [context, value] of previous) context._currentValue2 = value;}
    });
    await host.mount(); return {host, get tree() {return tree;}};
  }
  for (const kind of ['author', 'book_series']) {
    state.theme = createTheme({isTV: true, width: 1920});
    let phase = 'error', retries = 0;
    let snapshot = {phase, error: {code: 'request_failed'}, sections: [], finished: false};
    const entry = {id: 'author-1', kind, libraryId: 'books', title: 'Author'};
    const controller = {getSnapshot: () => snapshot.phase === phase ? snapshot : (snapshot = {...snapshot, phase}),
      subscribe: () => () => {}, load: async () => {retries++; phase = 'ready';}};
    const props = {controller, entry, runtime: {artwork: () => undefined, ambient: () => undefined}, actions: {menu() {}}, openEntry() {}, onBack() {}};
    const screen = await mount(AuthorSeriesScreen, props), regionElement = ofType(screen.tree, FocusRegion)[0]!;
    const error = await mount(AudiobookErrorState, ofType(screen.tree, AudiobookErrorState)[0]!.props);
    const view = await mount(StateView, (error.tree as Element).props), action = await mount(StateActionButton, ofType(view.tree, StateActionButton)[0]!.props);
    const provider = await mount(FocusProvider, {children: null}), providerElement = provider.tree as Element;
    const registryContext: [any, unknown] = [providerElement.type, providerElement.props.value];
    const outer = await mountWithContexts(FocusRegion, regionElement.props, [registryContext]);
    const region = await mountWithContexts((outer.tree as Element).type, (outer.tree as Element).props, [registryContext]);
    const guide = region.tree as Element, regionContext: [any, unknown] = [(guide.props.children as Element).type, regionElement.props.id];
    const contexts: [any, unknown][] = [registryContext, regionContext];
    const mounted: Awaited<ReturnType<typeof mountWithContexts>>[] = [];
    try {
      const registry = providerElement.props.value;
      guide.props.ref({nativeTag: 101, setDestinations() {}, requestTVFocus() {}});
      let node = action.tree as Element;
      while (typeof node.type !== 'string') {
        const child = await mountWithContexts(node.type, node.props, contexts); mounted.push(child); node = child.tree as Element;
      }
      assert.equal(node.type, 'Pressable');
      const retryHost = {nativeTag: 102, requestTVFocus() {}};
      node.props.ref(retryHost);
      assert.equal(registry.child({regionId: regionElement.props.id, childId: regionElement.props.entry}), retryHost);
      assert.equal(registry.entryTarget(regionElement.props.id), retryHost);
      node.props.onPress(); assert.equal(retries, 1);
      await screen.update(props); assert.equal(ofType(screen.tree, AudiobookErrorState).length, 0);
    } finally {
      for (const child of mounted.reverse()) await child.host.unmount();
      await region.host.unmount(); await outer.host.unmount(); await provider.host.unmount(); await action.host.unmount(); await view.host.unmount(); await error.host.unmount(); await screen.host.unmount();
    }
  }
});

test('actual Book and Author Resume buttons stay admitted during tail loading and retained error', async () => {
  const {screenModule} = await import('./helpers/screen-harness.mjs');
  const {BookScreen} = await import('../src-next/screens/destinations/audiobooks/BookScreen.tsx');
  for (const kind of ['book', 'author']) for (const outcome of ['ready', 'error']) {
    state.theme = createTheme({isTV: true, width: 1920});
    const id = `${kind}-1`, entry = {id, kind, libraryId: 'books', title: 'Title'};
    const row = {id: 'offered-book', kind: 'book', libraryId: 'books', title: 'Book',
      bookProgress: {bookId: 'offered-book', itemId: 'file-41', partPositionMs: 120000, positionMs: 5520000}};
    const listening = {target: {kind: 'book', id, libraryId: 'books'}, actions: ['play'], resume: {itemId: 'file-41', positionSeconds: 120}, resumeUnavailable: false};
    const sectionId = kind === 'book' ? 'files' : 'books';
    const section = {id: sectionId, type: 'list', heading: {key: sectionId}, entries: kind === 'book' ? [] : [row], totalCount: 81, nextCursor: 'next'};
    let snapshot: any = {phase: 'ready', error: null, sections: [section], appended: [section], pagination: {cursor: null},
      projection: {entity: entry, listening, sections: [section]}};
    let resolveTail!: () => void, listener = () => {};
    class Service {
      subscribe = (fn: () => void) => {listener = fn; return () => {};}; getSnapshot = () => snapshot; select = async () => {};
      next = async () => {snapshot = {...snapshot, phase: 'loading'}; listener(); await new Promise<void>(resolve => {resolveTail = resolve;});
        snapshot = outcome === 'error' ? {...snapshot, phase: 'error', error: {code: 'timeout', retryable: true}}
          : {...snapshot, phase: 'ready', pagination: {cursor: 'next'}, projection: {sections: []},
            appended: [{...section, entries: [...section.entries, {id: 'extra', kind: 'book', title: 'Extra'}], nextCursor: ''}]}; listener();};
      dispose = () => {};
    }
    const starts: any[] = [];
    const {NativeAudiobookController} = await screenModule(new URL('../src-next/screens/destinations/audiobooks/model.ts', import.meta.url), {
      react: {default: {}}, '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
      '../../../../../../packages/client-core/src/library-content': {LibraryContentService: Service},
      '../../../../../../packages/client-core/src/container-state': {getContainerState: async () => ({revision: 1, watched: false})},
    });
    const value = new NativeAudiobookController({context: {api: {}, playback: {start: async (input: any) => {starts.push(input);}}}, kind, libraryId: 'books', id});
    const abort = new AbortController(), lease = {signal: abort.signal, isCurrent: () => !abort.signal.aborted};
    await value.load(lease);
    const facade = {getSnapshot: value.getSnapshot, subscribe: value.subscribe, load: () => value.load(lease),
      resume: () => value.resume(lease), startOver: () => value.startOver(lease), resumeBook: (book: any) => value.resumeBook(lease, book),
      next: (section: string) => value.next(section, lease), recover: () => value.recover(lease)};
    const screen = await mount(kind === 'book' ? BookScreen : AuthorSeriesScreen, {controller: facade, entry,
      runtime: {artwork: () => undefined, ambient: () => undefined, reportError() {}}, actions: {menu() {}}, openEntry() {}, onBack() {}});
    try {
      const pending = value.next(sectionId, lease);
      await screen.update({controller: facade, entry, runtime: {artwork: () => undefined, ambient: () => undefined, reportError() {}}, actions: {menu() {}}, openEntry() {}, onBack() {}});
      const button = ofType(screen.tree, Button).find(button => button.props.focusId === `${kind === 'book' ? 'book' : 'audiobook'}-${id}.resume`)!;
      assert.ok(button); assert.notEqual(button.props.disabled, true); button.props.onPress();
      await Promise.resolve(); assert.equal(starts.length, 1);
      assert.deepEqual(starts[0].target, {itemId: 'file-41', startSeconds: 120});
      resolveTail(); await pending;
      assert.equal(starts[0].lifetime.isCurrent(), true, 'a resident accepted offer survives an unrelated tail result');
      if (outcome === 'error') {button.props.onPress(); await Promise.resolve(); assert.equal(starts.length, 2);}
      snapshot = {...snapshot, phase: 'ready', pagination: {cursor: null}, projection: {entity: {...entry}, listening: {...listening}, sections: [section]}, appended: [section]};
      await value.load(lease);
      assert.equal(starts[0].lifetime.isCurrent(), false, 'an explicit replacement retires the old offer');
    } finally {await screen.host.unmount(); value.dispose();}
  }
});
