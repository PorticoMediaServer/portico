import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {registerHooks} from 'node:module';
import React, {type ReactElement, type ReactNode} from 'react';
import {transformSync} from 'rolldown/utils';
import {createTheme, fontFamilies, nativeType} from '../src-next/kit/theme.ts';

// Native hosts, scheduler admission and animation clocks are controlled here.
// The actual family components, callback closures and lifecycle run unchanged;
// device layout, decoding, gestures and D-pad focus require Android evidence.
const state = {
  theme: createTheme({isTV: false, width: 390}), fontFamilies, nativeType,
  slot: {active: true, recovering: false, gaveUp: false, cached: false, attempt: 0, loaded() {}, failed() {}},
  timings: [] as {toValue: number; duration: number}[],
  workletTargets: [] as number[],
  menuKeyEnabled: false,
};
Object.assign(globalThis, {__porticoFamilyRenderTest: state});
const data = (value: string) => `data:text/javascript,${encodeURIComponent(value)}`;
const rnStub = new URL('./react-native-stub.mjs', import.meta.url).href;
const native = data(`export * from ${JSON.stringify(rnStub)};
  export const View='View',Text='Text',Image='Image',TextInput='TextInput',Pressable='Pressable',Modal='Modal',ScrollView='ScrollView',FlatList='FlatList',VirtualizedList='VirtualizedList',TVFocusGuideView='TVFocusGuideView',ActivityIndicator='ActivityIndicator';
  export const Platform={OS:'android',isTV:false},PixelRatio={get:()=>1},Alert={alert(){}},BackHandler={addEventListener:()=>({remove(){}})};
  export const TVEventControl={enableTVMenuKey(){globalThis.__porticoFamilyRenderTest.menuKeyEnabled=true;},disableTVMenuKey(){globalThis.__porticoFamilyRenderTest.menuKeyEnabled=false;}};
  export const useWindowDimensions=()=>({width:globalThis.__porticoFamilyRenderTest.theme.width,height:globalThis.__porticoFamilyRenderTest.theme.isTV?1080:844,scale:1,fontScale:1});
  const flatten=s=>Array.isArray(s)?Object.assign({},...s.map(flatten)):s??{};
  export const StyleSheet={create:s=>s,flatten,absoluteFill:{position:'absolute',inset:0},absoluteFillObject:{position:'absolute',inset:0}};
  class Value {constructor(value){this.value=value} setValue(value){this.value=value} stopAnimation(){}}
  export const Animated={View:'AnimatedView',Value,timing:(_,config)=>{globalThis.__porticoFamilyRenderTest.timings.push(config);return {start(){}}}};`);
const animated = data(`import {useRef} from ${JSON.stringify(import.meta.resolve('react'))};
  export const useSharedValue=value=>useRef({value}).current,useAnimatedStyle=fn=>fn(),cancelAnimation=()=>{},withTiming=value=>{globalThis.__porticoFamilyRenderTest.workletTargets.push(value);return value;},withSpring=value=>value,withDelay=(_,value)=>value,runOnJS=fn=>fn,runOnUI=fn=>fn,useAnimatedRef=()=>useRef(null),useAnimatedScrollHandler=()=>{},useAnimatedReaction=()=>{},scrollTo=()=>{};
  export const Easing={bezier:()=>v=>v,linear:v=>v},ReduceMotion={Never:'never'};
  export default {View:'AnimatedView',ScrollView:'AnimatedScrollView',createAnimatedComponent:c=>c};`);
const theme = data(`const s=globalThis.__porticoFamilyRenderTest;export const activeTextScale=()=>s.theme.textScale;export const useTheme=()=>s.theme,fontFamilies=s.fontFamilies,nativeType=s.nativeType,ThemeProvider='ThemeProvider';`);
const pipeline = data(`export const useArtworkPlacement=()=>({row:0,column:0}),useArtworkSlot=()=>globalThis.__porticoFamilyRenderTest.slot,sizedUri=uri=>uri;`);
const gesture = data(`const pan=()=>new Proxy({}, {get:()=>()=>pan()});export const Gesture={Pan:pan,Native:pan,Race:pan,Simultaneous:pan};export const GestureDetector='GestureDetector';`);
const svg = data(`export default 'Svg';export const Defs='Defs',LinearGradient='LinearGradient',RadialGradient='RadialGradient',Rect='Rect',Stop='Stop',Path='Path',Circle='Circle',Ellipse='Ellipse',Line='Line',Polyline='Polyline',Polygon='Polygon',G='G',ClipPath='ClipPath',Mask='Mask',Use='Use',Text='SvgText';`);
registerHooks({resolve(specifier, context, next) {
  if (specifier === 'react-native') return {url: native, shortCircuit: true};
  if (specifier === 'react-native-reanimated') return {url: animated, shortCircuit: true};
  if (specifier === 'react-native-gesture-handler') return {url: gesture, shortCircuit: true};
  if (specifier === 'react-native-svg') return {url: svg, shortCircuit: true};
  if (specifier === '@react-native-community/blur') return {url: data(`export const BlurView='BlurView';`), shortCircuit: true};
  if (specifier === 'react-native-safe-area-context') return {url: data(`export const useSafeAreaInsets=()=>({top:0,bottom:0,left:0,right:0});`), shortCircuit: true};
  if (specifier.startsWith('.') && context.parentURL?.startsWith('file:')) {
    const pathname = new URL(specifier, context.parentURL).pathname;
    if (/\/kit\/theme(?:\.ts)?$/.test(pathname)) return {url: theme, shortCircuit: true};
    if (/\/platform\/ArtworkPipeline(?:\.ts)?$/.test(pathname)) return {url: pipeline, shortCircuit: true};
  }
  try {return next(specifier, context);} catch (error) {
    if (!['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT'].includes((error as NodeJS.ErrnoException).code ?? '') || !specifier.startsWith('.')) throw error;
    for (const suffix of ['.ts', '.tsx', '/index.ts', '/index.tsx']) {
      try {return next(specifier + suffix, context);} catch (retry) {if (!['ERR_MODULE_NOT_FOUND', 'ERR_UNSUPPORTED_DIR_IMPORT'].includes((retry as NodeJS.ErrnoException).code ?? '')) throw retry;}
    }
    throw error;
  }
}, load(url, context, next) {
  if (!url.endsWith('.tsx')) return next(url, context);
  const file = fileURLToPath(url), result = transformSync(file, readFileSync(file, 'utf8'), {lang: 'tsx', jsx: {runtime: 'automatic'}, target: 'es2022'});
  if (result.errors.length) throw new AggregateError(result.errors);
  return {format: 'module', source: result.code, shortCircuit: true};
}});
const {SettingsNav, SettingsSearchResults, ShortcutTiles} = await import('../src-next/kit/shell/SettingsShell.tsx');
const {Focusable} = await import('../src-next/kit/controls/Focusable.tsx');
type Element = ReactElement<Record<string, any>>;
function nodes(node: ReactNode): Element[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  return React.isValidElement<Record<string, any>>(node) ? [node, ...nodes(node.props.children)] : [];
}
function texts(tree: ReactNode) {return nodes(tree).filter(node => node.type === 'Text').map(node => node.props.children).filter(value => typeof value === 'string');}
const summaries = [
  {id: 'dashboard', label: 'Dashboard', icon: 'i-pulse' as const, badge: '2', warning: true, value: '2 playing'},
  {id: 'libraries', label: 'Libraries', icon: 'i-grid' as const, badge: '3', warning: true, value: '5'},
  {id: 'people', label: 'People', icon: 'i-people' as const, badge: '1', warning: true},
  {id: 'troubleshooting', label: 'Troubleshooting', icon: 'i-wrench' as const, badge: '2'},
  {id: 'segments', label: 'Intros and credits', icon: 'i-next' as const, badge: '7'},
  {id: 'live', label: 'Live TV & Channels', icon: 'i-tv' as const, value: '14 channels'},
];
const states = [{id: 'remote', label: 'Remote access', icon: 'i-link' as const, stateValue: 'On'}, {id: 'state-with-digits', label: 'Configured state', icon: 'i-cog' as const, stateValue: 'HTTP/2'}];
const accountPages = [{id: 'playback', label: 'Playback', icon: 'i-play' as const}];
const serverPages = [...summaries, ...states, {id: 'shared', label: 'Shared page', icon: 'i-server' as const, audience: 'administrators' as const}];
for (const isTV of [false, true]) {
  test(`${isTV ? 'TV' : 'phone'}: actual Settings rows suppress summaries, preserve explicit states, and show only a known major dot`, () => {
    state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
    const props = {accountPages, serverPages, viewerRole: 'owner' as const, serverName: 'Actual server', selectedId: 'libraries', onNavigate(_id: string) {}, mode: 'combined' as const};
    const tree = SettingsNav(props), rendered = texts(tree);
    for (const count of ['1', '2', '3', '5', '7', '2 playing', '14 channels']) assert.ok(!rendered.includes(count), count);
    for (const value of ['On', 'HTTP/2', ...summaries.map(page => page.label)]) assert.ok(rendered.includes(value), value);
    assert.equal(nodes(tree).filter(node => node.props.accessibilityRole === 'image').length, 0);
    const major = SettingsNav({...props, serverPages: [...serverPages, {id: 'storage', label: 'Storage & backups', icon: 'i-disk', majorIssue: true}]});
    const dots = nodes(major).filter(node => node.props.accessibilityRole === 'image');
    assert.equal(dots.length, 1); assert.equal(dots[0].props.accessibilityLabel, 'Needs attention');
    assert.equal(dots[0].props.style.backgroundColor, state.theme.colors.record);
    assert.equal(dots[0].props.style.width, isTV ? 10 : 7);
    const storage = nodes(major).find(node => node.type === Focusable && node.props.accessibilityLabel === 'Storage & backups')!;
    assert.ok(nodes(storage).includes(dots[0]));
  });
  test(`${isTV ? 'TV' : 'phone'}: actual row callbacks, selected state and role audiences remain intact`, () => {
    state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
    const calls: string[] = [];
    const props = {accountPages, serverPages, viewerRole: 'owner' as const, serverName: 'Actual server', selectedId: 'libraries', onNavigate: (id: string) => calls.push(id), mode: 'combined' as const, nextFocusRight: 123};
    const row = nodes(SettingsNav(props)).find(node => node.type === Focusable && node.props.accessibilityLabel === 'Libraries')!;
    assert.equal(row.props.accessibilityState.selected, true); assert.equal(row.props.nextFocusRight, 123);
    row.props.onPress(); if (isTV) row.props.onFocus(); else assert.equal(row.props.onFocus, undefined);
    assert.deepEqual(calls, isTV ? ['libraries', 'libraries'] : ['libraries']);
    const member = texts(SettingsNav({...props, viewerRole: 'member'}));
    assert.ok(member.includes('Playback')); assert.ok(!member.includes('Dashboard')); assert.ok(!member.includes('Shared page'));
    const admin = texts(SettingsNav({...props, viewerRole: 'administrator'}));
    assert.ok(admin.includes('Shared page')); assert.ok(!admin.includes('Dashboard'));
  });
  test(`${isTV ? 'TV' : 'phone'}: shortcuts retain callbacks without summary badges`, () => {
    state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
    let calls = 0;
    const props = {actions: [{id: 'notifications', label: 'Notifications', icon: 'i-bell' as const, onPress: () => calls++}], unreadCounts: {notifications: 7}};
    const tree = ShortcutTiles(props); assert.deepEqual(texts(tree), ['Notifications']);
    nodes(tree).find(node => node.type === Focusable)!.props.onPress(); assert.equal(calls, 1);
  });
}
test('actual search result grouping preserves its content match count and current values', () => {
  const calls: unknown[] = [];
  const matches = [{address: 'account/playback', pageTitle: 'Playback', rowId: 'skip', label: 'Skip intro'}, {address: 'account/playback', pageTitle: 'Playback', rowId: 'credits', label: 'Skip credits'}] as unknown as Parameters<typeof SettingsSearchResults>[0]['matches'];
  const tree = SettingsSearchResults({matches, query: 'Skip', values: {skip: 'Automatic'}, onNavigate: (...args) => calls.push(args)});
  assert.ok(texts(tree).includes('2')); assert.ok(texts(tree).includes('Automatic'));
  nodes(tree).find(node => node.type === Focusable)!.props.onPress(); assert.deepEqual(calls, [['account/playback', 'skip']]);
});
