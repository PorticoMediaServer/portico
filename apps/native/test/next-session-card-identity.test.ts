import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {registerHooks} from 'node:module';
import React, {type ReactElement, type ReactNode} from 'react';
import {transformSync} from 'rolldown/utils';
import {createTheme, fontFamilies, nativeType} from '../src-next/kit/theme.ts';
import {sessionViewerParts} from '../src-next/kit/cards/session-identity.ts';
import type {SessionCardProps} from '../src-next/kit/cards/ListeningCards';

// Controlled hosts inspect the actual SessionCard and Parts; no device/layout claim.
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
  if (specifier === '@react-native-masked-view/masked-view') return {url: data(`export default 'MaskedView';`), shortCircuit: true};
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
const {SessionCard} = await import('../src-next/kit/cards/ListeningCards.tsx');
const {Parts} = await import('../src-next/kit/cards/primitives.tsx');
type Element = ReactElement<Record<string, any>>;
function nodes(node: ReactNode): Element[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  if (!React.isValidElement<Record<string, any>>(node)) return [];
  return [node, ...nodes(node.props.children)];
}
function who(props: Partial<SessionCardProps>) {
  const card = SessionCard({title: 'Supplied title', identity: [], facts: [], ...props} as SessionCardProps);
  const art = card.props.children({focused: false, holding: false, hovered: false});
  const parts = nodes(art).find(node => node.type === Parts && node.props.parts.some((p: {id: string}) => p.id === 'viewer'))!;
  assert.ok(parts, 'Actual SessionCard must render a person line');
  return {parts: parts.props.parts, tree: Parts(parts.props)};
}
const flatten = (style: any): Record<string, any> => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};

test('supplied native identity leads with genuine username and retains quieter profile then device', () => {
  for (const isTV of [false, true]) {
    state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
    const viewer = Object.freeze({name: 'Kids', username: 'sam_k', device: 'Living room TV'});
    const line = who({viewer, facts: [{id: 'viewer', text: 'Old profile', role: 'identity'}, {id: 'account', text: 'Opaque account', role: 'context'}, {id: 'device', text: 'Old device', role: 'context'}]});
    assert.deepEqual(line.parts.map((p: {text: string}) => p.text), ['sam_k', 'Kids', 'Living room TV']);
    const texts = nodes(line.tree).filter(node => node.type === 'Text');
    const primary = flatten(texts[0].props.style), profile = flatten(texts[1].props.style);
    assert.equal(primary.fontFamily, state.theme.type.captionStrong.fontFamily);
    assert.equal(primary.color, state.theme.colors.textSecondary);
    assert.equal(profile.color, state.theme.colors.textTertiary);
    assert.notEqual(primary.color, profile.color);
    assert.equal(viewer.name, 'Kids');
  }
});

test('native comparison suppresses trim/case-only repeats and keeps differing profile spelling', () => {
  for (const name of ['sam_k', ' SAM_K ', 'Sam_K']) {
    assert.deepEqual(who({viewer: {name, username: 'sam_k', device: 'TV'}}).parts.map((p: {text: string}) => p.text), ['sam_k', 'TV']);
  }
  assert.deepEqual(sessionViewerParts({name: ' Kids ', username: 'sam_k', device: 'TV'}).map(p => p.text), ['sam_k', ' Kids ', 'TV']);
});

test('native missing genuine username stays profile-only without promoting an account or ID', () => {
  for (const username of [undefined, '', '  ']) {
    assert.deepEqual(who({viewer: {name: 'Sam', username, device: 'TV'}}).parts.map((p: {text: string}) => p.text), ['Sam', 'TV']);
  }
  assert.deepEqual(sessionViewerParts({name: '  ', username: 'sam_k', device: 'TV'}).map(p => p.text), ['sam_k', 'TV']);
});

test('native facts-only callers retain the actual supplied labels and roles', () => {
  const facts = [{id: 'viewer', text: 'Sam', role: 'identity'}, {id: 'account', text: 'Existing account label', role: 'context'}, {id: 'device', text: 'on TV', role: 'context'}] as const;
  assert.deepEqual(who({facts}).parts, facts);
});
