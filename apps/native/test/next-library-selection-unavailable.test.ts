import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {NativeLibraryDestination} from '../src-next/screens/library/model.ts';
import * as selectionUnavailableActions from '../src-next/screens/library/selection-unavailable-actions.ts';
import * as wholeQueryUnavailable from '../src-next/screens/library/whole-query-unavailable.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import type {BrowseRequestBody} from '../../../packages/client-core/src/browse.ts';

const unavailable = {reason: 'search_not_bounded'} as const;
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const library = {id:'lib',name:'Movies',kind:'movie'};
const pivot = {id:'movies',labelKey:'library.movies',entityKinds:['movie'],defaultSort:[{field:'title',direction:'asc'}],supportedViews:['grid'],browsable:true,sorts:['title'],filters:[]};
const caps = {library:{...library,defaultView:'browse'},pivots:[pivot],resolvedPivot:pivot,fields:[],quickFilters:[],
 sorts:[{id:'title',labelKey:'sort.title',directions:['asc','desc'],defaultDirection:'asc',applicableKinds:['movie']}],
 queryLimits:{maximumDepth:5,maximumClauses:40,maximumBytes:65536,maximumSorts:3,defaultLimit:60,maximumLimit:100,cursorTtlMs:1800000}};

test('actual native Library model publishes unavailable alongside resident rows and clears it on ready/query/retirement', async () => {
 let current=true;
 const counts={play:1,queue:1,playlist:1,myList:1,watched:1,collection:1,favorite:1,download:0,edit:1,refresh:1,delete:1};
 const api:LibraryContentApi={request:async<T>()=>caps as T,requestRaw:async(_path,method,options)=>{
  assert.equal(method,'POST');const body=options?.body as BrowseRequestBody;
  const ready=body.sort?.[0]?.direction==='desc';
  return {status:200,headers:{},body:{pivot:'movies',applied:{query:body.query??null,sort:body.sort,seek:null},
   entries:[{id:'movie',libraryId:'lib',kind:'movie',title:'Movie',navigation:{view:'item'}}],
   pageInfo:{start:0,total:1,revision:'r1',hasMore:false,nextCursor:''},
   ...(ready?{wholeSelection:{selector:{query:{libraryId:'lib',pivot:'movies',sort:body.sort,selectionBasis:'a'.repeat(64)}},catalogRevision:'r1',total:1,
    types:[{kind:'movie',count:1,capabilityCounts:counts,libraries:[{libraryId:'lib',count:1}]}]}}:{wholeSelectionUnavailable:unavailable})}};
 }};
 const model=new NativeLibraryDestination({library,scope:{serverId:'server',viewerId:'viewer'},current:()=>current,api});
 try {
  await model.load();await tick();await tick();
  assert.equal(model.getSnapshot().phase,'ready');
  assert.deepEqual(model.getSnapshot().wholeSelectionUnavailable,unavailable);
  assert.equal(model.getSnapshot().collection?.itemAt(0)?.id,'movie');
  model.setQuery([],{field:'title',direction:'desc'});
  assert.equal(model.getSnapshot().wholeSelectionUnavailable,undefined);
  await tick();await tick();assert.equal(model.getSnapshot().wholeSelection?.total,1);assert.equal(model.getSnapshot().wholeSelectionUnavailable,undefined);
  model.setQuery([],{field:'title',direction:'asc'});
  assert.equal(model.getSnapshot().wholeSelection,undefined);await tick();await tick();assert.ok(model.getSnapshot().wholeSelectionUnavailable);
  current=false;assert.equal(model.getSnapshot().wholeSelectionUnavailable,undefined);
 } finally {model.dispose();}
});

test('actual native Library selection composes the refusal without an executor and redraws on metadata/query changes', async () => {
 const host=hooks(), listeners=new Set<()=>void>(); let notifications=0,disposed=0;
 let sourceState:any={phase:'ready',library,tab:{id:'browse'},revision:'r1',queryRevision:0,collection:{},wholeSelectionUnavailable:unavailable};
 const summary={count:1,every:{play:true},types:[{kind:'movie',count:1}],library:{kind:'single',libraryId:'lib'}};
 const selectionState={active:true,summary,selectedKeys:[],target:{mode:'explicit',items:[]}};
 const producer={model:{getSnapshot:()=>selectionState,subscribe:()=>()=>{},clear(){}},whole:()=>undefined,dispose(){disposed++;}};
 const source={getSnapshot:()=>sourceState,subscribe:(fn:()=>void)=>{listeners.add(fn);return()=>listeners.delete(fn);}};
 const {createNativeLibrarySelection}=await screenModule(new URL('../src-next/screens/library/selection.tsx',import.meta.url),{
    './selection-actions': {nativeSelectionActionInput: () => {throw new Error('unexpected query action');}}, '../downloads/request': {}, '../../platform/offline-downloads': {},
  react:host.react,'react-native':{Text:'Text',View:'View'},'react-native-safe-area-context':{useSafeAreaInsets:()=>({top:0,bottom:0})},
  '../../../../../packages/i18n/src':{defaultI18n:{t:(key:string)=>key}},
  '../../../../../packages/client-core/src/presentation/errors':{},
  '../../../../../packages/client-core/src/catalogue-query-selection':{},
  '../../../../../packages/client-core/src/selection-model':{selectionFourthAction:()=>undefined},
  '../../../../../packages/client-core/src/media-selection':{},
  '../../shell/selection-display':{selectedItemsLabel:()=> '1 movie'},
  '../../kit/selection':{SelectionBar:'SelectionBar',SelectionPhoneActionBar:'SelectionPhoneActionBar'},
  '../../kit/theme':{useTheme:()=>({isTV:true,layers:{navigation:1}})},'../../kit/overlays':{},'../../kit/controls':{},
  '../destinations/lifetime':{nativeDestinationLifetime:()=>({current:()=>true})},
  '../native-screen-services':{nativeScreenServices:()=>({})},'../destinations/DestinationPicker':{},'../search/loaded-destinations':{},'../destinations/bulk-media':{},
  './model':{nativeLibraryEntryKey:()=>''},'./play-selection':{createNativeLibraryPlaySelection:()=>producer},
  './selection-unavailable-actions':selectionUnavailableActions,'./whole-query-unavailable':wholeQueryUnavailable,
 });
 const port=createNativeLibrarySelection({subscribeAuthority:()=>()=>{}} as any,source as any,{getSnapshot:()=>({visible:true}),subscribe:()=>()=>{}} as any);
 const off=port.subscribe(()=>{notifications++;});
 const nodes=(value:any):any[]=>Array.isArray(value)?value.flatMap(nodes):value?.props?[value,...nodes(value.props.children)]:[];
 const bar=()=>{const element=port.render() as any;return nodes(host.render(()=>element.type(element.props))).find(n=>n.type==='SelectionBar');};
 try {
  port.start!(); const first=bar();assert.equal(first.props.count,1);
  assert.deepEqual(first.props.selectAllUnavailable,{label:'next.selection.selectAllUnavailableLabel',reason:'next.selection.selectAllSearchNotBounded'});
  assert.equal(first.props.onSelectAll,undefined);assert.equal(first.props.selectAllLabel,undefined);assert.ok(first.props.actions.some((a:any)=>a.id==='play'));
  const before=notifications;
  sourceState={...sourceState,wholeSelectionUnavailable:undefined};listeners.forEach(fn=>fn());assert.ok(notifications>before);
  assert.equal(bar().props.selectAllUnavailable,undefined);assert.equal(disposed,0,'metadata-only publication preserves page selection');
  sourceState={...sourceState,wholeSelectionUnavailable:unavailable};listeners.forEach(fn=>fn());assert.ok(bar().props.selectAllUnavailable);
  sourceState={...sourceState,queryRevision:1,wholeSelectionUnavailable:undefined};listeners.forEach(fn=>fn());assert.equal(bar(),undefined);assert.equal(disposed,1);
 } finally {off();port.dispose();}
 assert.equal(listeners.size,0);
});
