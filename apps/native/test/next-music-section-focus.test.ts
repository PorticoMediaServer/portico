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
const stateKey = '__porticoMusicSectionFocusTest';
Object.assign(globalThis, {[stateKey]: state});
const moduleURL = (source: string) => `data:text/javascript,${encodeURIComponent(source)}`;
const nativeStub = new URL('./react-native-stub.mjs', import.meta.url).href;
const native = moduleURL(`export * from ${JSON.stringify(nativeStub)};
  export const Platform={OS:'android',isTV:true};
  export const View='View',Text='Text',TextInput='TextInput',Pressable='Pressable',Modal='Modal',ScrollView='ScrollView',FlatList='FlatList',TVFocusGuideView='TVFocusGuideView',ActivityIndicator='ActivityIndicator';
  export const Image='Image',PixelRatio={get:()=>1},Animated={View:'AnimatedView'};
  export const TVEventControl={enableTVMenuKey(){},disableTVMenuKey(){}}; export const BackHandler={addEventListener:()=>({remove(){}})};
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
    if(specifier.endsWith('/kit/states'))return {url:new URL('../src-next/kit/states/StateView.tsx',import.meta.url).href,shortCircuit:true};
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
const {StateView} = await import('../src-next/kit/states/StateView.tsx');
const {StateActionButton} = await import('../src-next/kit/states/StateActionButton.tsx');
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



const {MusicErrorView}=await import('../src-next/screens/destinations/music/MusicErrorView.tsx');
const {MusicSectionControls}=await import('../src-next/screens/destinations/music/MusicSectionControls.tsx');
const {createFocusRegistry}=await import('../src-next/kit/focus/registry.ts');
const {FocusProvider}=await import('../src-next/kit/focus/FocusRegion.tsx');

async function nativeChain(element:Element){
 const chain:Awaited<ReturnType<typeof mount>>[]=[];let node=element;
 while(typeof node.type!=='string'){const instance=await mount(node.type,node.props);chain.push(instance);node=instance.tree as Element;}
 return {node,chain,async close(){for(const instance of chain.reverse())await instance.host.unmount();}};
}

async function realRegion(){
 const registry=createFocusRegistry(),provider=await mount(FocusProvider,{registry,children:null});
 const context=(provider.tree as Element).type as unknown as {_currentValue2:unknown},original=context._currentValue2;
 context._currentValue2=(provider.tree as Element).props.value;
 const outer=await mount(FocusRegion,{id:'music-list',order:['music-next-artist-all'],children:null});
 const region=await mount((outer.tree as Element).type,(outer.tree as Element).props);
 const nested=elements(region.tree).find(element=>element.props.value==='music-list')!;
 const regionContext=nested.type as unknown as {_currentValue2:unknown},prior=regionContext._currentValue2;
 regionContext._currentValue2=nested.props.value;
 return {registry,async close(){regionContext._currentValue2=prior;await region.host.unmount();await outer.host.unmount();context._currentValue2=original;await provider.host.unmount();}};
}

test('music paging Next retains its real Focusable host in loading and becomes Retry in place after failure',async ()=>{
 const section={phase:'ready',items:[{id:'song'}],offset:0,nextCursor:'real-cursor',canNext:true,canPrevious:true};
 let commands=0;const props={section,id:'artist-all',next:()=>{commands++;},previous:()=>{},retry:()=>{commands++;},refresh:()=>{}};
 const bound=await realRegion();const controls=await mount(MusicSectionControls,props);let next:Awaited<ReturnType<typeof nativeChain>>|undefined;
 try{
  let button=ofType(controls.tree,Button).find(node=>node.props.testID==='music-next-artist-all')!;
  assert(button);next=await nativeChain(button);assert.equal(next.node.type,'Pressable');assert.equal(next.node.props.onPress,props.next);
  const host={requestTVFocus(){}};next.node.props.ref(host);next.node.props.onFocus({nativeEvent:{}});assert.equal(bound.registry.child({regionId:'music-list',childId:'music-next-artist-all'}),host);
  // Keep the same Button/Focusable hook hosts across publication, just as React
  // does for their stable element type/key. Only the native endpoint is stubbed.
  await controls.update({...props,section:{...section,phase:'loading'}});
  button=ofType(controls.tree,Button).find(node=>node.props.testID==='music-next-artist-all')!;
  assert.equal(button.props.focusableWhenDisabled,true);assert.equal(button.props.disabled,true);
  let current=button;for(const instance of next.chain){await instance.update(current.props);current=instance.tree as Element;}
  assert.equal(current.props.disabled,false);assert.notEqual(current.props.focusable,false);assert.equal(current.props.onPress,undefined);assert.equal(current.props.testID,next.node.props.testID);assert.equal(bound.registry.child({regionId:'music-list',childId:'music-next-artist-all'}),host);
  await controls.update({...props,section:{...section,phase:'error',canNext:false,error:Object.assign(new Error('busy'),{code:'server_busy',status:503,retryable:true})}});
  button=ofType(controls.tree,Button).find(node=>node.props.testID==='music-next-artist-all')!;
  assert.equal(button.props.onPress,props.retry);button.props.onPress();assert.equal(commands,1);
  current=button;for(const instance of next.chain){await instance.update(current.props);current=instance.tree as Element;}assert.equal(bound.registry.child({regionId:'music-list',childId:'music-next-artist-all'}),host);assert.equal(bound.registry.capture()?.childId,'music-next-artist-all');
  const error=ofType(controls.tree,MusicErrorView)[0]!;assert.equal(error.props.testID,'music-error-artist-all');assert.equal(error.props.retryAction,false);
  await controls.update({...props,section:{...section,phase:'error',canNext:false,error:Object.assign(new Error('denied'),{status:403,code:'forbidden'})}});
  button=ofType(controls.tree,Button).find(node=>node.props.testID==='music-next-artist-all')!;assert.equal(button.props.disabled,true);assert.equal(button.props.focusableWhenDisabled,true);current=button;for(const instance of next.chain){await instance.update(current.props);current=instance.tree as Element;}assert.equal(current.props.onPress,undefined);assert.equal(bound.registry.child({regionId:'music-list',childId:'music-next-artist-all'}),host);
 }finally{await next?.close();await controls.host.unmount();await bound.close();}
});

test('simultaneous music section and Similar errors yield different real StateView action host identities',async ()=>{
 const failure=Object.assign(new Error('busy'),{status:503,code:'server_busy',retryable:true});
 const ids:string[]=[];
 for(const testID of ['music-error-album-songs','music-error-artist-songs','music-error-artist-releases','music-similar-artist-error']){
  const error=await mount(MusicErrorView,{testID,error:failure,onRetry:()=>{}});
  try{const viewElement=ofType(error.tree,StateView)[0]!;const view=await mount(StateView,viewElement.props);
   try{const action=ofType(view.tree,StateActionButton)[0]!;assert(action);const chain=await nativeChain(action);try{ids.push(chain.node.props.testID);}finally{await chain.close();}}
   finally{await view.host.unmount();}
  }finally{await error.host.unmount();}
 }
 assert.equal(new Set(ids).size,4);
});

test('a real focused row host prevents section eviction until focus transfers to the retained Next control',async()=>{
 const {MusicSectionPager}=await import('../src-next/screens/destinations/music/paging.ts');let calls=0;
 const pager=new MusicSectionPager<{id:string}>({current:()=>true,publish:()=>{},read:async cursor=>{calls++;const n=Number(cursor||0);return {items:[{id:String(n)}],nextCursor:n<4?String(n+1):'',offset:n,total:5,revision:'r'};}});
 await pager.load();await pager.next(pager.getSnapshot());await pager.next(pager.getSnapshot());const original=pager.getSnapshot(),row=original.items[0];
 const bound=await realRegion(),rowHost={requestTVFocus(){}},nextHost={requestTVFocus(){}};
 const rendered=await nativeChain(React.createElement(Focusable,{focusId:'original-song',onFocus:()=>pager.focus(row,original),onBlur:()=>pager.blur(row,original),onPress:()=>{}}) as Element);
 const next=await nativeChain(React.createElement(Button,{focusId:'music-next-artist-all',onPress:()=>{void pager.next(pager.getSnapshot());}},'Next') as Element);
 try{
  rendered.node.props.ref(rowHost);rendered.node.props.onFocus({nativeEvent:{}});
  assert.equal(bound.registry.child({regionId:'music-list',childId:'original-song'}),rowHost);assert.equal(bound.registry.capture()?.childId,'original-song');
  await pager.next(original);assert.equal(calls,3);assert.equal(pager.getSnapshot().items[0],row);assert.equal(bound.registry.child({regionId:'music-list',childId:'original-song'}),rowHost);
  rendered.node.props.onBlur({nativeEvent:{}});next.node.props.ref(nextHost);next.node.props.onFocus({nativeEvent:{}});next.node.props.onPress();for(let i=0;i<10;i++)await Promise.resolve();
  assert.equal(calls,4);assert.equal(pager.getSnapshot().items.includes(row),false);assert.equal(bound.registry.capture()?.childId,'music-next-artist-all');assert.equal(bound.registry.child({regionId:'music-list',childId:'music-next-artist-all'}),nextHost);
  await pager.previous(pager.getSnapshot());assert.equal(pager.getSnapshot().offset,0);assert.equal(bound.registry.capture()?.childId,'music-next-artist-all');
 }finally{await rendered.close();await next.close();await bound.close();pager.dispose();}
});
