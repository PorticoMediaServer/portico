import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {registerHooks} from 'node:module';
import {transformSync} from 'rolldown/utils';
import {fabricHost, React} from './helpers/fabric-host.mjs';

/** FX-173 / BLK-TV-006 authored binding vectors. This isolated file installs the
 * supported TSX/native-host loader the shared kit expects: the installed production
 * React instance the Fabric reconciler itself uses, rolldown's TSX transform, and
 * the kit's native host surface as real host types. Only native hosts/transport and
 * the animation/gesture drivers are controlled, so the region below runs its own
 * child identity check and registration against the product's own components. No
 * product context or test-only export is involved. Element state is UNRUN here. */
type TestGlobals = typeof globalThis & {__DEV__?: boolean; __porticoBindingReact?: typeof React};
const globals = globalThis as TestGlobals;
globals.__DEV__ = false;
globals.__porticoBindingReact = React;
const moduleURL = (source: string) => `data:text/javascript,${encodeURIComponent(source)}`;
const react = moduleURL(`const R=globalThis.__porticoBindingReact;export default R;export const {${Object.keys(React).filter(key => /^[A-Za-z_$][\w$]*$/.test(key)).join(',')}}=R;`);
const native = moduleURL(`export * from ${JSON.stringify(new URL('./react-native-stub.mjs', import.meta.url).href)};
 export const View='View',Text='Text',Image='Image',TextInput='TextInput',Pressable='Pressable',Modal='Modal',ScrollView='ScrollView',FlatList='FlatList',VirtualizedList='VirtualizedList',TVFocusGuideView='TVFocusGuideView',ActivityIndicator='ActivityIndicator',RefreshControl='RefreshControl',Switch='Switch',StatusBar='StatusBar';
 export const Platform={OS:'android',isTV:false,Version:36,constants:{Manufacturer:'test'}},PixelRatio={get:()=>1,getFontScale:()=>1},Dimensions={get:()=>({width:1920,height:1080,scale:1,fontScale:1}),addEventListener:()=>({remove(){}})};
 export const useWindowDimensions=()=>({width:1920,height:1080,scale:1,fontScale:1});
 const flatten=s=>Array.isArray(s)?Object.assign({},...s.map(flatten)):s??{};
 export const StyleSheet={create:s=>s,flatten,absoluteFill:{position:'absolute',inset:0},absoluteFillObject:{position:'absolute',inset:0},hairlineWidth:1};
 export const AccessibilityInfo={isReduceMotionEnabled:async()=>true,addEventListener:()=>({remove(){}}),announceForAccessibility(){}};
 export const AppState={currentState:'active',addEventListener:()=>({remove(){}})},BackHandler={addEventListener:()=>({remove(){}})},Keyboard={dismiss(){}},Alert={alert(){}},Share={share(){}},I18nManager={isRTL:false};
 export const Linking={openURL:async()=>{},canOpenURL:async()=>true,addEventListener:()=>({remove(){}}),getInitialURL:async()=>null};
 export const NativeModules={},NativeEventEmitter=class{addListener(){return{remove(){}}}},DeviceEventEmitter=new NativeEventEmitter();
 export const TVEventControl={enableTVMenuKey(){},disableTVMenuKey(){}},TVEventHandler={addListener:()=>({remove(){}})},useTVEventHandler=()=>{};
 export const findNodeHandle=node=>node?.tag??node?.nativeTag??null,UIManager={hasViewManagerConfig:()=>true},requireNativeComponent=name=>name,registerCallableModule=()=>{},AppRegistry={registerComponent(){}},useColorScheme=()=> 'dark';
 export const PanResponder={create:()=>({panHandlers:{}})};
 class Value{constructor(value){this.value=value}setValue(value){this.value=value}stopAnimation(){}};
 export const Easing={bezier:()=>x=>x,linear:x=>x,out:x=>x,in:x=>x,inOut:x=>x};
 export const Animated={View:'AnimatedView',Text:'AnimatedText',ScrollView:'AnimatedScrollView',Image:'AnimatedImage',Value,timing:(value,config)=>({start:done=>{value.value=config.toValue;done?.({finished:true});}}),parallel:()=>({start(){}}),sequence:()=>({start(){}}),createAnimatedComponent:x=>x};
`);
const animated = moduleURL(`import React from ${JSON.stringify(react)};
 export const useSharedValue=value=>React.useRef({value}).current,useAnimatedStyle=fn=>fn(),useAnimatedRef=()=>React.useRef(null),useAnimatedScrollHandler=fn=>fn,useDerivedValue=fn=>({value:fn()}),cancelAnimation=()=>{},scrollTo=()=>{},runOnJS=fn=>fn,runOnUI=fn=>fn;
 export const useAnimatedReaction=(prepare,reaction)=>{const prior=React.useRef(null);React.useEffect(()=>{const next=prepare();reaction(next,prior.current);prior.current=next;});};
 export const withTiming=(value,config,done)=>{if(done)setImmediate(()=>done(true));return value},withSpring=withTiming,withDelay=(_,value)=>value,withDecay=()=>0;
 export const Easing={bezier:()=>x=>x,linear:x=>x,out:x=>x},ReduceMotion={Never:'never'};export default{View:'AnimatedView',ScrollView:'AnimatedScrollView',Image:'AnimatedImage',createAnimatedComponent:x=>x};`);
const svgNames = ['Svg', 'Defs', 'Pattern', 'LinearGradient', 'RadialGradient', 'Rect', 'Stop', 'Mask', 'Path', 'G', 'ClipPath', 'Circle', 'Ellipse', 'Line', 'Polygon', 'Polyline', 'Use', 'Image', 'Text'];
const svg = moduleURL(`export default 'Svg';export const ${svgNames.map(name => `${name}='${name}'`).join(',')};`);
const gesture = moduleURL(`const pan=()=>new Proxy({},{get:(_,key)=>key==='then'?undefined:()=>pan()});export const Gesture={Pan:pan,Native:pan,Race:pan,Simultaneous:pan,Tap:pan,LongPress:pan},GestureDetector='GestureDetector',GestureHandlerRootView='GestureHandlerRootView';`);
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === 'react') return {url: react, shortCircuit: true};
    if (specifier === 'react-native') return {url: native, shortCircuit: true};
    if (specifier === 'react-native-reanimated') return {url: animated, shortCircuit: true};
    if (specifier === 'react-native-gesture-handler') return {url: gesture, shortCircuit: true};
    if (specifier === 'react-native-svg') return {url: svg, shortCircuit: true};
    if (specifier === '@react-native-community/blur') return {url: moduleURL(`export const BlurView='BlurView';`), shortCircuit: true};
    if (specifier === '@react-native-masked-view/masked-view') return {url: moduleURL(`export default 'MaskedView';`), shortCircuit: true};
    if (specifier === 'react-native-safe-area-context') return {url: moduleURL(`export const SafeAreaProvider='SafeAreaProvider',SafeAreaView='SafeAreaView',useSafeAreaInsets=()=>({top:0,bottom:0,left:0,right:0});`), shortCircuit: true};
    return next(specifier, context);
  },
  load(url, context, next) {
    if (!url.endsWith('.tsx')) return next(url, context);
    const file = fileURLToPath(url);
    const result = transformSync(file, readFileSync(file, 'utf8'), {lang: 'tsx', jsx: {runtime: 'classic'}, target: 'es2022'});
    if (result.errors.length) throw new AggregateError(result.errors, `Cannot transform ${file}`);
    return {format: 'module', source: result.code, shortCircuit: true};
  },
});

const {createFocusRegistry, focusTestID} = await import('../src-next/kit/focus/registry.ts');
const {FocusProvider, FocusRegion} = await import('../src-next/kit/focus/FocusRegion.tsx');
const {StateActionButton} = await import('../src-next/kit/states/StateActionButton.tsx');
const {StateView} = await import('../src-next/kit/states/StateView.tsx');
const {ThemeProvider} = await import('../src-next/kit/theme.ts');
const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime.ts');
const {TVPeoplePanel} = await import('../src-next/screens/settings-live/server/TVPeoplePanel.tsx');
const {routePath} = await import('../../../packages/contracts/src/routes.ts');
const {defaultI18n} = await import('../../../packages/i18n/src/index.ts');

type Registry = ReturnType<typeof createFocusRegistry>;
type Node = {type: string; props: Record<string, any>; children: Node[]};
const flatten = (nodes: readonly Node[] | Node): Node[] => (Array.isArray(nodes) ? nodes : [nodes]).flatMap(node => [node, ...flatten(node.children ?? [])]);
const nodesOf = (root: readonly Node[]) => flatten(root as Node[]);
const hostWithTestID = (root: readonly Node[], testID: string) => nodesOf(root).find(node => node.props?.testID === testID);
const tree = (registry: Registry, content: React.ReactNode) =>
  React.createElement(ThemeProvider, {width: 1920}, React.createElement(FocusProvider, {registry}, content));
const region = (id: string, children: React.ReactNode) =>
  React.createElement(FocusRegion, {id, entry: 'retry', enabled: true, testID: id}, children);
/** One retry action, either with a caller prefix or with none at all. */
const retry = (onPress: () => void, extra: Record<string, any> = {}) => ({id: 'retry', label: 'Retry', onPress, ...extra});
const derived = (prefix: string) => focusTestID(prefix, JSON.stringify(['action', 'retry']));

test('a prefix-less state action registers in its actual region and presses', async () => {
  const host = await fabricHost(), registry = createFocusRegistry();
  let pressed = 0;
  try {
    await host.render(tree(registry, region('tv.people.failure', React.createElement(StateActionButton, {action: retry(() => {pressed++;})}))));
    assert.deepEqual(registry.order('tv.people.failure'), ['retry'], 'the region attached the caller-owned semantic id');
    assert.ok(registry.child({regionId: 'tv.people.failure', childId: 'retry'}), 'the old missing-ID throw is gone');
    // controlID(undefined) is undefined, so the StateView-less action keeps the
    // semantic id and the derived host id is focusTestID(region, 'retry').
    const pressable = hostWithTestID(host.root, focusTestID('tv.people.failure', 'retry'));
    assert.ok(pressable, 'the derived host id is the semantic action id, not a serialized action tuple');
    assert.equal(pressable!.props.accessibilityRole, 'button');
    pressable!.props.onPress();
    assert.equal(pressed, 1);
    pressable!.props.onPress();
    assert.equal(pressed, 2, 'each press is delivered, not swallowed by the region');
  } finally {await host.unmount();}
});

test('a named prefix and a native-start identity keep their existing region child ids', async () => {
  const host = await fabricHost(), registry = createFocusRegistry();
  try {
    await host.render(tree(registry, region('tv.library.failure', React.createElement(StateActionButton, {testID: 'tv.library.failure', action: retry(() => {})}))));
    assert.ok(hostWithTestID(host.root, derived('tv.library.failure')), 'the derived prefixed id is unchanged');
    assert.ok(registry.child({regionId: 'tv.library.failure', childId: derived('tv.library.failure')}), 'the prefixed child is attached under that id');
    await host.render(tree(registry, region('native-start', React.createElement(StateActionButton, {testID: 'native-start', action: retry(() => {})}))));
    assert.ok(hostWithTestID(host.root, derived('native-start')), 'native-start keeps its own derived entry identity');
    assert.ok(registry.child({regionId: 'native-start', childId: derived('native-start')}));
  } finally {await host.unmount();}
});

test('the same mounted action keeps its region identity and eligibility through busy', async () => {
  const host = await fabricHost(), registry = createFocusRegistry();
  const build = (loading: boolean, disabled: boolean) => tree(registry,
    region('tv.people.failure', React.createElement(StateActionButton, {testID: 'tv.people.failure', loading, disabled, action: retry(() => {})})));
  try {
    await host.render(build(false, false));
    const identity = derived('tv.people.failure');
    const attached = registry.child({regionId: 'tv.people.failure', childId: identity});
    assert.ok(attached, 'the enabled action is attached');
    assert.notEqual(hostWithTestID(host.root, identity)!.props.disabled, true);
    await host.render(build(true, true));
    assert.ok(hostWithTestID(host.root, identity), 'busy draws the same region child identity');
    assert.equal(registry.child({regionId: 'tv.people.failure', childId: identity}), undefined, 'a busy action is not eligible');
    assert.equal(hostWithTestID(host.root, identity)!.props.disabled, true);
    await host.render(build(false, false));
    assert.ok(hostWithTestID(host.root, identity), 'enabled keeps that identity on the same binding');
    assert.equal(registry.child({regionId: 'tv.people.failure', childId: identity}), attached, 'the same native child is reattached, not replaced');
  } finally {await host.unmount();}
});

test('two sibling regions never share a retry identity', async () => {
  const host = await fabricHost(), registry = createFocusRegistry();
  try {
    await host.render(tree(registry, React.createElement(React.Fragment, null,
      region('tv.people.failure', React.createElement(StateActionButton, {action: retry(() => {})})),
      region('tv.library.failure', React.createElement(StateActionButton, {action: retry(() => {})})))));
    const people = registry.child({regionId: 'tv.people.failure', childId: 'retry'});
    const library = registry.child({regionId: 'tv.library.failure', childId: 'retry'});
    assert.ok(people && library);
    assert.notEqual(people, library, 'each region owns its own retry host');
    assert.ok(hostWithTestID(host.root, focusTestID('tv.people.failure', 'retry')));
    assert.ok(hostWithTestID(host.root, focusTestID('tv.library.failure', 'retry')));
  } finally {await host.unmount();}
});

test('the error StateView composition inside a region registers its prefix-less retry', async () => {
  const host = await fabricHost(), registry = createFocusRegistry();
  try {
    await host.render(tree(registry, region('tv.server.people',
      React.createElement(StateView, {title: 'Error', primaryAction: retry(() => {})}))));
    assert.ok(registry.child({regionId: 'tv.server.people', childId: 'retry'}), 'the caller-owned StateView prefix is absent here, so the semantic id is the child identity');
    assert.ok(hostWithTestID(host.root, focusTestID('tv.server.people', 'retry')), 'the derived host id is focusTestID(region, semantic id)');
  } finally {await host.unmount();}
});

/** The real panel: actual NativeScreenLifetime + createTVServerReader from the
 * reader-test primary-owner fixture, and a controlled API that rejects exactly the
 * three People GETs. That reaches the screen's own error branch, so the runtime's
 * error presenter supplies the retry the shared StateActionButton draws inside the
 * real region. No module error state, admission stub or context export is involved. */
test('the actual TVPeoplePanel failure retry registers in its region and re-reads on press', async () => {
  const host = await fabricHost(), registry = createFocusRegistry();
  const authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  const peoplePaths: ReadonlySet<string> = new Set<string>([routePath('get_admin_access_members'), routePath('get_admin_access_invitations'), routePath('get_admin_access_devices')]);
  let reads = 0;
  const api = {request: async <T,>(path: string): Promise<T> => {
    if (peoplePaths.has(path.split('?')[0])) {
      const query = new URL(path, 'https://fixture.invalid').searchParams;
      assert.equal(query.get('limit'), '50', 'the real People read stays bounded');
      assert.deepEqual([...query.keys()].sort(), path.startsWith(routePath('get_admin_access_invitations')) ? ['limit', 'state'] : ['limit']);
      if (query.has('state')) assert.equal(query.get('state'), 'pending');
      reads++; throw Object.assign(new Error('people_unreachable'), {code: 'unreachable'});}
    throw Object.assign(new Error('unexpected_read'), {code: 'unexpected'});
  }};
  const ports = {context: {account: {canManage: true, account: {role: 'owner', primaryProfileId: 'primary', disabled: false}},
    api: api as unknown as Parameters<typeof TVPeoplePanel>[0]['ports']['context']['api']}};
  const runtime = {text: {t: defaultI18n.t, locale: 'en-US'},
    error: (_error: unknown, again: () => void) => ({title: 'Something went wrong', primaryAction: {id: 'retry', label: 'Try again', onPress: again}})};
  try {
    await host.render(tree(registry, region('tv.server.people', React.createElement(TVPeoplePanel as any, {ports, runtime, lifetime}))));
    for (let attempt = 0; attempt < 8 && reads === 0; attempt++) await host.settle();
    assert.equal(reads, 3, 'the panel issues exactly the three People reads');
    const retries = ['members', 'invitations', 'devices'].map(section => focusTestID(`tv-people-${section}-error`, JSON.stringify(['action', 'retry'])));
    assert.deepEqual(registry.order('tv.server.people'), retries, 'each failed section owns its own retry');
    for (const childId of retries) assert.ok(registry.child({regionId: 'tv.server.people', childId}), 'the retry is a live region child');
    const pressable = hostWithTestID(host.root, retries[0]);
    assert.ok(pressable, 'the real failure retry carries the derived region id');
    pressable!.props.onPress();
    for (let attempt = 0; attempt < 8 && reads < 4; attempt++) await host.settle();
    assert.equal(reads, 4, 'the section retry repeats only its own bounded read');
  } finally {lifetime.dispose(); await host.unmount();}
});
