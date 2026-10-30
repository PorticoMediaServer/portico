// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as policy from '../../../packages/client-core/src/server-admin/recording-policy';
import * as model from '../src-next/screens/settings-live/recording-owner-model';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.footer), ...nodes(node.props.pickerContext), ...nodes(node.props.action)] : [];
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
const owner = (id: string) => ({owner: {authority: 'local' as const, accountId: 'a', profileId: id}, accountName: 'A', profileName: id, canRecord: true, revision: 1});

async function loadPresenter(passive?: Array<()=>void>) {
  const effect=passive?{useEffect:(create:()=>void|(()=>void),deps?:readonly unknown[])=>React.useEffect(()=>{
    let retired=false,cleanup:void|(()=>void);passive.push(()=>{if(!retired)cleanup=create();});
    return()=>{retired=true;passive.push(()=>{if(typeof cleanup==='function')cleanup();});};
  },deps)}:{};
  return screenModule(new URL('../src-next/screens/settings-live/LiveOwnerRecording.tsx', import.meta.url), {
    react: {...React, default: React,...effect}, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/server-admin/recording-policy': policy,
    '../../../../../packages/client-core/src/server-admin/live-words': {tunerStatus: () => ({}), tunerUseLine: () => ''},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
    '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/theme': {useTheme: () => createTheme({isTV: true, width: 1280})},
    './recording-owner-model': model, './server/AdvancedSection': {NativeAdvancedSection: 'Advanced'},
  });
}

async function presenter() {
  const {NewRuleSheet} = await loadPresenter();
  const pending = deferred<any>(); const calls: string[] = [];
  // Each profile records by its own switch; nothing is inherited from the account.
  const first = {owners: Array.from({length:200},(_,i)=>owner(`p${i+1}`)), grants: Array.from({length:400},(_,i)=>({owner:owner(`p${i+1}`).owner,enabled:true,revision:1})), nextCursor: 'c1'};
  const controller = {current:()=>true,lifetime: {current: () => true,signal:new AbortController().signal}, policy: async (cursor = '') => {calls.push(cursor); return cursor ? pending.promise : first;}, load: async () => {}, getSnapshot: () => ({list: {sources: []}}), guideSeries: async () => [], createRule: async () => {}};
  const runtime = {navigation: {getSnapshot: () => ({revision: 7})}, getSnapshot: () => ({contentVisible: true}), text: {t: (key: string) => key}, error: () => ({})};
  let props: any = {controller, runtime, open: true, onOpenChange: () => {}, onSaved: () => {}}; let tree: any;
  const host = createFabricHookHost(() => {tree = NewRuleSheet(props);});
  await act(() => host.mount());
  const select = () => nodes(tree).find(row => row.props.testID === 'live-owner-new-rule-owner');
  const change = async (next: any) => {props = {...props, ...next}; await act(() => host.rerender());};
  return {host, select, change, pending, calls, policy:first};
}

test('cached owner picker retires old More across close and reopen', async () => {
  const f = await presenter();
  try {
    const oldMore = f.select().props.pickerContext.props.onPress;
    await act(() => {oldMore();}); assert.deepEqual(f.calls, ['', 'c1']);
    await f.change({open: false}); await f.change({open: true});
    await act(() => {oldMore();}); assert.deepEqual(f.calls, ['', 'c1']);
    await act(() => {f.pending.resolve({owners: [owner('old')], grants: [], nextCursor: ''});});
    assert.equal(f.select().props.options.some((option: any) => String(option.value).includes('old')), false);
  } finally {await f.host.unmount();}
});

test('owner and rule presenters follow their own three-page cursors and retry the failed cursor', async () => {
  const {NativeRecordingOwner, NewRuleSheet} = await loadPresenter(); const calls: string[] = []; let refreshed = false;
  const rules = (n: number) => ({id: `r${n}`, revision: 1, owner: owner('p1').owner, status: '', kind: 'series', name: `Rule ${n}`, match: `m${n}`, sourceId: 's', enabled: false, options: {prePaddingMs: 0, postPaddingMs: 0, keepPolicy: null, newEpisodesOnly: false, priority: 0, folderTemplate: ''}});
  let fail = true;
  const controller = {current:()=>true,lifetime: {current: () => true,signal:new AbortController().signal}, tuners: async () => ({sources: [], upcomingWindowHours: 0}),
    policy: async (cursor = '') => {calls.push(`o:${cursor}`); if (refreshed && cursor === '') return {owners: [owner('new')], grants: [{owner: owner('new').owner, enabled: false, revision: 2}], nextCursor: 'fresh-owner'}; if (cursor === 'o2' && fail) {fail = false; throw Error('temporary');} return {owners: Array.from({length:cursor==='o2'?1:200},(_,i)=>owner(`p${(cursor===''?0:cursor==='o1'?200:400)+i+1}`)), grants: [], nextCursor: cursor === '' ? 'o1' : cursor === 'o1' ? 'o2' : ''};},
    rules: async (cursor = '') => {calls.push(`r:${cursor}`); if (refreshed && cursor === '') return {items: [rules(10)], nextCursor: 'fresh-rule'}; return {items: Array.from({length:cursor==='r2'?1:100},(_,i)=>rules((cursor===''?0:cursor==='r1'?100:200)+i+1)), nextCursor: cursor === '' ? 'r1' : cursor === 'r1' ? 'r2' : ''};},
    dvrSettings: async () => ({revision: 1, settings: {}, enumerations: {}})};
  const recordings = {storage: async () => ({policy: {}, measurement: {usedBytes: 0, freeBytes: 0, reservedBytes: 0, writeHealthy: true}, forecastHours: 0})};
  const runtime = {navigation: {getSnapshot: () => ({revision: 7})}, getSnapshot: () => ({contentVisible: true}), text: {t: (key: string) => key}, error: () => ({})};
  let tree: any; const host = createFabricHookHost(() => {tree = NativeRecordingOwner({controller, recordings, runtime, sources: []});});
  const find = (id: string) => nodes(tree).find(row => row.props.testID === id);
  try {
    await act(() => host.mount());
    await act(() => {find('live-owner-owners-more').props.onPress();});
    assert.ok(nodes(tree).some(row => row.props.policy?.owners?.some((choice: any) => choice.owner.profileId === 'p201')));
    await act(() => {find('live-owner-owners-more').props.onPress();});
    assert.ok(find('live-owner-page-retry'));
    await act(() => {find('live-owner-page-retry').props.onPress();});
    await act(() => {find('live-owner-rules-more').props.onPress();});
    assert.ok(find('live-owner-rule-r101'));
    await act(() => {find('live-owner-rules-more').props.onPress();});
    assert.ok(find('live-owner-rule-r201'));
    assert.deepEqual(calls, ['o:', 'r:', 'o:o1', 'o:o2', 'o:o2', 'r:r1', 'r:r2']);
    refreshed = true;
    await act(() => {find('live-owner-new-rule').props.onPress();});
    const sheet = nodes(tree).find(row => row.type === NewRuleSheet);
    await act(() => {sheet.props.onSaved();});
    assert.ok(find('live-owner-rule-r10')); assert.equal(find('live-owner-rule-r101'), undefined);
    assert.ok(nodes(tree).some(row => row.props.policy?.grants?.some((grant: any) => grant.owner.profileId === 'new' && !grant.enabled)));
    assert.equal(nodes(tree).some(row => row.props.policy?.owners?.some((choice: any) => choice.owner.profileId === 'p201')), false);
    await act(() => {find('live-owner-rules-more').props.onPress();});
    assert.ok(calls.includes('r:fresh-rule'));
  } finally {await host.unmount();}
});

test('actual sheet Select submits one minute and an old Save cannot settle a reopened sheet', async () => {
  const {NewRuleSheet} = await loadPresenter();
  const pending = deferred<void>(); const bodies: any[] = []; let saved = 0;
  const selected = owner('p1'), grants = [{owner: selected.owner, enabled: true, revision: 1}];
  const {NativeLiveOwnerController} = await import('../src-next/screens/settings-live/live-owner-controller');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime');
  const authority = {authority: () => ({serverId: 'server', accountId: 'a', profileId: 'p1', epoch: 'e', viewerFence: 'v'}), current: () => true, subscribeAuthority: () => () => {}};
  const api = {request: async <T,>(path: string, method?: string, body?: unknown) => {
    if (method === 'POST') {bodies.push(body); await pending.promise;const command=body as any;return {serverId:'server',protocolVersion:'1.0',result:{id:'created-rule',revision:1,status:'ok',owner:command.owner,kind:command.kind,name:command.name,match:command.match,sourceId:command.sourceId,enabled:command.enabled,options:{...command.options,prePaddingMs:command.options.prePaddingMs??0,postPaddingMs:command.options.postPaddingMs??0,keepPolicy:command.options.keepPolicy??{mode:'keep-all',keepCount:0,keepDays:0,keepUntilWatched:false}}}} as T;}
    if (path.startsWith('/v1/guide?')) {const q = new URLSearchParams(path.split('?')[1]); const start = q.get('start')!, end = q.get('end')!; const at = new Date(Date.parse(start) + 3600000).toISOString(), until = new Date(Date.parse(start) + 7200000).toISOString(); return {protocolVersion: '1.0', serverId: 'server', guide: {viewerFence: 'v', start, end, timezone: 'UTC', observedAt: start, nextCursor: '', sources: [{id: 's', name: 'Source', generation: 'g', publishedAt: start, availableStart: start, availableEnd: end, provenance: 'live-source', refreshState: ''}], channels: [{id: 'c', sourceId: 's', provenance: 'live-source', name: 'Channel', generation: 'g', preferenceRevision: 0, recordAvailable: true, programmes: [{id: 'p', channelId: 'c', title: 'A title', start: at, end: until, seriesId: ''}]}]}} as T;}
    return {protocolVersion: '1.0', serverId: 'server', sources: [], refresh: []} as T;
  }};
  const controller = new NativeLiveOwnerController(new NativeScreenLifetime(authority), api, () => 'op1');
  const runtime = {navigation: {getSnapshot: () => ({revision: 7})}, getSnapshot: () => ({contentVisible: true}), text: {t: (key: string) => key}, error: () => ({})};
  let props: any = {controller, runtime, policy: {owners: [selected], grants}, sources: [{id: 's', name: 'Source'}], open: true, onOpenChange: () => {}, onSaved: () => {saved++;}}; let tree: any;
  const host = createFabricHookHost(() => {tree = NewRuleSheet(props);});
  const find = (id: string) => nodes(tree).find(row => row.props.testID === id);
  try {
    await act(() => host.mount());
    await act(() => {find('live-owner-new-rule-series').props.onValueChange(JSON.stringify(['s', 'c', 'g', 'p']));});
    await act(() => {find('live-owner-new-rule-before').props.onValueChange('60');});
    const save = nodes(tree.props.footer).find(row => row.type === 'Button' && row.props.children === 'action.save').props.onPress;
    await act(() => {save();});
    assert.equal(bodies.length, 1); assert.equal(bodies[0].kind, 'title'); assert.equal(bodies[0].match, 'A title'); assert.equal(bodies[0].options.prePaddingMs, 60000);
    props = {...props, open: false}; await act(() => host.rerender());
    props = {...props, open: true}; await act(() => host.rerender());
    await act(() => {save();}); assert.equal(bodies.length, 1);
    await act(() => {pending.resolve();}); assert.equal(saved, 0);
  } finally {await host.unmount();}
});

test('owner picker continuation registers in a strict focus region', async () => {
  const registry = await import('../src-next/kit/focus/registry');
  const hostRefs = await import('../src-next/kit/focus/useHostRef');
  const contexts: {current: unknown}[] = [];
  const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react: {...React, default: React, createContext: (current: unknown) => {const value = {current}; contexts.push(value); return value;}, useContext: (context: {current: unknown}) => context.current},
    'react-native': {findNodeHandle: () => null, TVFocusGuideView: 'Guide', View: 'View'},
    '../theme': {useTheme: () => ({isTV: true}), nativeType: () => ({})}, './registry': registry, './useHostRef': hostRefs,
  });
  const f = await presenter();
  try {
    const button = f.select().props.pickerContext;
    const focusRegistry = registry.createFocusRegistry(); focusRegistry.configure({id: 'dvr-picker', orientation: 'vertical'});
    contexts[0]!.current = focusRegistry; contexts[1]!.current = 'dvr-picker';
    const target = createFabricHookHost(() => {focus.useFocusTarget({focusId: button.props.focusId, testID: button.props.testID, disabled: button.props.disabled});});
    await target.mount(); await target.unmount();
    assert.equal(button.props.focusId, 'live-owner-picker-more');
  } finally {await f.host.unmount();}
});

test('cached picker retires More on unmount even while its controller remains live', async () => {
  const f = await presenter();
  await f.change({open: false}); await f.change({open: true});
  const more = f.select().props.pickerContext.props.onPress;
  await act(() => {more();}); assert.deepEqual(f.calls, ['', 'c1']);
  await f.host.unmount();
  await act(() => {more(); f.pending.resolve({owners: [owner('old')], grants: [], nextCursor: ''});});
  assert.deepEqual(f.calls, ['', 'c1']);
});

test('old page handler cannot send its cursor through a replacement controller', async () => {
  const {NativeRecordingOwner} = await loadPresenter(); const calls: string[] = [];
  const make = (name: string) => ({current:()=>true,lifetime: {current: () => true,signal:new AbortController().signal}, tuners: async () => ({sources: [], upcomingWindowHours: 0}),
    policy: async (cursor = '') => {calls.push(`${name}:${cursor}`); return {owners: [owner(name)], grants: [], nextCursor: `${name}-cursor`};},
    rules: async () => ({items: [], nextCursor: ''}), dvrSettings: async () => ({revision: 1, settings: {}, enumerations: {}})});
  let controller = make('a'); const replacement = make('b');
  const recordings = {storage: async () => ({policy: {}, measurement: {usedBytes: 0, freeBytes: 0, reservedBytes: 0, writeHealthy: true}, forecastHours: 0})};
  const runtime = {navigation: {getSnapshot: () => ({revision: 7})}, getSnapshot: () => ({contentVisible: true}), text: {t: (key: string) => key}, error: () => ({})};
  let tree: any; const host = createFabricHookHost(() => {tree = NativeRecordingOwner({controller, recordings, runtime, sources: []});});
  try {
    await act(() => host.mount());
    const old = nodes(tree).find(row => row.props.testID === 'live-owner-owners-more').props.onPress;
    controller = replacement; await act(() => host.rerender());
    await act(() => {old();});
    assert.deepEqual(calls, ['a:', 'b:']);
    assert.ok(nodes(tree).some(row => row.props.policy?.owners?.some((choice: any) => choice.owner.profileId === 'b')));
  } finally {await host.unmount();}
});

const ruleRow=(n:number)=>({id:`r${n}`,revision:1,owner:owner('p1').owner,status:'disabled',kind:'series',name:`Rule ${n}`,match:`m${n}`,sourceId:'s',enabled:false,options:{prePaddingMs:0,postPaddingMs:0,keepPolicy:null,newEpisodesOnly:false,priority:0,folderTemplate:''}});
async function pageFixture(){
 const {NativeRecordingOwner,NewRuleSheet}=await loadPresenter();let generation=1,revision=7,visible=true,live=true,failRule=false;
 const life=new AbortController(),calls:string[]=[],holds=new Map<string,ReturnType<typeof deferred<any>>>();
 const key=(kind:string,cursor:string,gen=generation)=>`${kind}:${gen}:${cursor}`;
 const owners=(cursor:string,gen=generation)=>({owners:Array.from({length:cursor?200:200},(_,i)=>owner(`p${(gen-1)*1000+(cursor?200:0)+i+1}`)),grants:gen===1?Array.from({length:400},(_,i)=>({owner:owner(`p${i+1}`).owner,enabled:true,revision:gen})):[{owner:owner('p1').owner,enabled:false,revision:gen}],nextCursor:cursor?'':gen===1?'o1':'new-o1'});
 const rules=(cursor:string,gen=generation)=>({items:Array.from({length:100},(_,i)=>ruleRow((gen-1)*1000+(cursor?100:0)+i+1)),nextCursor:cursor?'':gen===1?'r1':'new-r1'});
 let controller={current:()=>live&&!life.signal.aborted,lifetime:{current:()=>live,signal:life.signal},tuners:async()=>({sources:[],upcomingWindowHours:0}),
  policy:async(cursor='')=>{const k=key('owners',cursor);calls.push(k);return holds.get(k)?.promise??owners(cursor);},
  rules:async(cursor='')=>{const k=key('rules',cursor);calls.push(k);if(cursor&&failRule){failRule=false;throw Error('retry original rule cursor');}return holds.get(k)?.promise??rules(cursor);},
  dvrSettings:async()=>({revision:1,settings:{prePaddingMs:0,postPaddingMs:0},enumerations:{}}),load:async()=>{},getSnapshot:()=>({list:{sources:[]}})};
 let recordings={storage:async()=>({policy:{reserveBytes:0},measurement:{usedBytes:0,freeBytes:0,reservedBytes:0,writeHealthy:true},forecastHours:0})};
 const runtime={navigation:{getSnapshot:()=>({revision})},getSnapshot:()=>({contentVisible:visible}),text:{t:(k:string)=>k},error:(_e:unknown,retry:()=>void)=>({primaryAction:{onPress:retry}})};
 let tree:any;const host=createFabricHookHost(()=>{tree=NativeRecordingOwner({controller,recordings,runtime,sources:[]});});await act(()=>host.mount());
 const find=(id:string)=>nodes(tree).find(node=>node.props.testID===id);
 return {host,calls,controller,tree:()=>tree,find,owners,rules,
  hold:(kind:string,cursor:string,gen=generation)=>{const pending=deferred<any>();holds.set(key(kind,cursor,gen),pending);return pending;},
  nextGeneration:()=>{generation++;},failRule:()=>{failRule=true;},
  refresh:async()=>{await act(()=>find('live-owner-new-rule').props.onPress());const sheet=nodes(tree).find(node=>node.type===NewRuleSheet);generation++;await act(()=>sheet.props.onSaved());},
  replaceRecordings:async()=>{recordings={...recordings};await act(()=>host.rerender());},
  retire:async(kind:'route'|'authority'|'controller')=>{if(kind==='route')revision++;else if(kind==='authority'){live=false;life.abort();}else{controller={...controller};generation++;}await act(()=>host.rerender());},
  dispose:async ()=>{await host.unmount();life.abort();}};
}

test('saved owners/rules More and failed Retry cannot borrow a freshly accepted head',async()=>{
 const f=await pageFixture();try{
  const oldOwners=f.find('live-owner-owners-more').props.onPress,oldRules=f.find('live-owner-rules-more').props.onPress;
  f.failRule();await act(()=>oldRules());const oldRetry=f.find('live-owner-page-retry').props.onPress;
  assert(f.find('live-owner-rule-r100'));await f.refresh();assert(!f.find('live-owner-rule-r1'));assert(f.find('live-owner-rule-r1001'));
  const count=f.calls.length;await act(()=>{oldOwners();oldRules();oldRetry();});assert.equal(f.calls.length,count);
  await act(()=>f.find('live-owner-owners-more').props.onPress());await act(()=>f.find('live-owner-rules-more').props.onPress());
  assert(f.calls.includes('owners:2:new-o1'));assert(f.calls.includes('rules:2:new-r1'));assert(f.find('live-owner-rule-r1200'));
 }finally{await f.dispose();}
});
for(const kind of ['owners','rules'] as const)for(const retirement of ['route','authority','controller'] as const)test(`pending ${kind} continuation cannot append after ${retirement} retirement`,async()=>{
 const f=await pageFixture(),cursor=kind==='owners'?'o1':'r1',pending=f.hold(kind,cursor);try{
  await act(()=>f.find(`live-owner-${kind}-more`).props.onPress());await f.retire(retirement);
  await act(()=>pending.resolve(kind==='owners'?f.owners(cursor,1):f.rules(cursor,1)));
  assert(!f.find('live-owner-rule-r101'));assert(!nodes(f.tree()).some(node=>node.props.policy?.owners?.some((row:any)=>row.owner.profileId==='p201')));
  if(retirement==='controller')assert(f.find('live-owner-rule-r1001'),'same controller with new recordings gets its fresh head');
 }finally{await f.dispose();}
});
for(const order of ['old-first','head-first'] as const)test(`fresh head owns pending chain completion in ${order} order`,async()=>{
 const f=await pageFixture(),tail=f.hold('owners','o1');try{
  const oldMore=f.find('live-owner-owners-more').props.onPress;await act(()=>oldMore());
  f.nextGeneration();const head=f.hold('owners','',2);await f.replaceRecordings();const count=f.calls.length;await act(()=>oldMore());assert.equal(f.calls.length,count);
  const old=()=>tail.resolve(f.owners('o1',1)),fresh=()=>head.resolve(f.owners('',2));
  if(order==='old-first'){await act(old);await act(fresh);}else{await act(fresh);await act(old);}
  const policy=nodes(f.tree()).find(node=>node.props.policy?.owners)?.props.policy;assert(policy);assert.equal(policy.owners.length,200);assert.equal(policy.grants[0].enabled,false);
  assert(policy.owners.every((row:any)=>Number(row.owner.profileId.slice(1))>1000));assert(f.find('live-owner-rule-r1001'));assert(!f.find('live-owner-rule-r1'));
 }finally{await f.dispose();}
});

test('actual permission/default/storage drafts block current and retained More without clearing a draft',async()=>{
 for(const type of ['permission','defaults','storage'] as const){const f=await pageFixture();try{
  const old=f.find('live-owner-owners-more').props.onPress;
  const section=nodes(f.tree()).find(node=>node.props.draft&&(type==='permission'?node.props.policy:type==='defaults'?node.props.document:node.props.storage));assert(section);
  const draft=type==='permission'?{enabled:false}:{...section.props.draft,...(type==='defaults'?{prePaddingMs:60000}:{reserveBytes:123})};
  await act(()=>type==='permission'?section.props.onChange(owner('p1').owner,draft):section.props.onChange(draft));
  assert.equal(f.find('live-owner-owners-more').props.disabled,true);const count=f.calls.length;await act(()=>{old();f.find('live-owner-owners-more').props.onPress();});assert.equal(f.calls.length,count);
  const retained=nodes(f.tree()).find(node=>node.props.draft&&(type==='permission'?node.props.policy:type==='defaults'?node.props.document:node.props.storage));
  assert.deepEqual(type==='permission'?retained.props.draft[policy.recordingOwnerKey(owner('p1').owner)]:retained.props.draft,draft);
 }finally{await f.dispose();}}
});

test('actual rule-page failure retains its full hundred-row head and Retry uses the same offered cursor',async()=>{
 const f=await pageFixture();try{
  f.failRule();await act(()=>f.find('live-owner-rules-more').props.onPress());assert.equal(nodes(f.tree()).filter(node=>node.type==='Focusable'&&String(node.props.testID).startsWith('live-owner-rule-r')).length,100);
  await act(()=>f.find('live-owner-page-retry').props.onPress());assert(f.find('live-owner-rule-r200'));
  assert.deepEqual(f.calls.filter(call=>call.startsWith('rules:')),['rules:1:','rules:1:r1','rules:1:r1']);
 }finally{await f.dispose();}
});

async function flushPassive(queue:Array<()=>void>){for(let pass=0;queue.length&&pass<20;pass++){const ready=queue.splice(0);await act(()=>ready.forEach(run=>run()));}assert.equal(queue.length,0);}
test('committed guide B withholds guide A before passive cleanup and only B can be selected',async()=>{
 const passive:Array<()=>void>=[],{NewRuleSheet}=await loadPresenter(passive),a=deferred<any[]>(),reads:string[]=[];
 const choice=(sourceId:string)=>({key:`${sourceId}-entry`,title:`${sourceId} title`,channelName:'Channel',startsAt:'2026-10-27T01:00:00Z',seriesId:'',recordAvailable:true,anchor:{sourceId,channelId:'c',generation:'g',programmeId:'p'}});
 const life=new AbortController(),selected=owner('p1');
 const controller={current:()=>true,lifetime:{current:()=>true,signal:life.signal},guideSeries:async(_day:string,sourceId:string)=>{reads.push(sourceId);return sourceId==='a'?a.promise:[choice('b')];}};
 const runtime={navigation:{getSnapshot:()=>({revision:7})},getSnapshot:()=>({contentVisible:true}),text:{t:(key:string)=>key},error:()=>({})};
 const props={controller,runtime,policy:{owners:[selected],grants:[{owner:selected.owner,enabled:true,revision:1}]},sources:[{id:'a',name:'A'},{id:'b',name:'B'}],open:true,onOpenChange:()=>{},onSaved:()=>{}};
 let tree:any;const host=createFabricHookHost(()=>{tree=NewRuleSheet(props);});const find=(id:string)=>nodes(tree).find(node=>node.props.testID===id);
 try{
  await act(()=>host.mount());await flushPassive(passive);assert.deepEqual(reads,['a']);
  const oldSelect=find('live-owner-new-rule-series').props.onValueChange;
  await act(()=>find('live-owner-new-rule-source').props.onValueChange('b'));
  // The actual source's useLayoutEffects have committed B. This test-local
  // wrapper defers only passive cleanup/create, preserving the reported gap;
  // it does not claim React scheduler or native rendering equivalence.
  await act(()=>a.resolve([choice('a')]));assert(!find('live-owner-new-rule-series').props.options.some((row:any)=>row.value==='a-entry'));
  await act(()=>oldSelect('a-entry'));assert.notEqual(find('live-owner-new-rule-series').props.value,'a-entry');assert.deepEqual(reads,['a']);
  await flushPassive(passive);assert.deepEqual(reads,['a','b']);assert(find('live-owner-new-rule-series').props.options.some((row:any)=>row.value==='b-entry'));
  await act(()=>find('live-owner-new-rule-series').props.onValueChange('b-entry'));assert.equal(find('live-owner-new-rule-series').props.value,'b-entry');
 }finally{await host.unmount();await flushPassive(passive);life.abort();}
});

test('original actual controller disposal inside runtime getter refuses page and sheet IO while shared lifetime stays live',async()=>{
 const {NativeRecordingOwner,NewRuleSheet}=await loadPresenter();
 const {NativeLiveOwnerController}=await import('../src-next/screens/settings-live/live-owner-controller');const {NativeScreenLifetime}=await import('../src-next/screens/settings-live/lifetime');
 for(const sheet of [false,true]){
  let requests=0,reads=0;const lifetime=new NativeScreenLifetime({authority:()=>({serverId:'server',accountId:'a',profileId:'p',epoch:'e',viewerFence:'v'}),current:()=>true,subscribeAuthority:()=>()=>{}});
  const controller=new NativeLiveOwnerController(lifetime,{request:async<T>()=>{requests++;throw Error('retired controller must not dispatch');}},()=> 'operation');
  const runtime={navigation:{getSnapshot:()=>({revision:7})},getSnapshot:()=>{reads++;controller.dispose();return {contentVisible:true};},text:{t:(key:string)=>key},error:()=>({})};
  // One recordings controller, as the live route supplies it: a fresh identity each render would
  // be a new admission and not the disposal this test is about.
  const recordings={storage:async()=>{requests++;throw Error('retired page');}};
  let tree:any;const host=createFabricHookHost(()=>{tree=sheet?NewRuleSheet({controller,runtime,open:true,onOpenChange:()=>{},onSaved:()=>{}}):NativeRecordingOwner({controller,runtime,recordings,sources:[]});});
  try{await act(()=>host.mount());assert(reads>0);assert.equal(controller.current(),false);assert.equal(lifetime.current(),true);assert.equal(requests,0);assert.equal(tree,null);}
  finally{await host.unmount();lifetime.dispose();}
 }
});

test('real owner Select retains its chosen row and original opener through a genuine page append',async()=>{
 const {Select}=await screenModule(new URL('../src-next/kit/controls/Select.tsx',import.meta.url),{
  react:{...React,default:React},'react-native':{FlatList:'FlatList',Text:'Text',View:'View',StyleSheet:{create:(value:unknown)=>value}},
  '../../../../../packages/i18n/src/index':{defaultI18n},'../Icon':{Icon:'Icon'},'../overlays/surface':{Dialog:'Dialog'},
  '../theme':{useTheme:()=>createTheme({isTV:true,width:1280}),fontFamilies:{700:'Font'},nativeType:()=>({})},'./Focusable':{Focusable:'Focusable'},
  './control-model':await import('../src-next/kit/controls/control-model'),'./useControlValue':await import('../src-next/kit/controls/useControlValue')});
 const f=await presenter();let props=f.select().props,outer:any,tree:any;
 // Preserve the actual Select/NativeSelect hook hosts and native opener across
 // the changed offered options. Only platform endpoints and Dialog are inert.
 const outerHost=createFabricHookHost(()=>{outer=Select.render(props,null);});await outerHost.mount();const NativeSelect=outer.type;
 const innerHost=createFabricHookHost(()=>{tree=NativeSelect.render(outer.props,null);});await innerHost.mount();
 const rerender=async()=>{props=f.select().props;await act(()=>outerHost.rerender());await act(()=>innerHost.rerender());};
 try{
  const trigger=nodes(tree).find(node=>node.type==='Focusable'),nativeOpener={requestTVFocus(){}};trigger.props.ref(nativeOpener);
  await act(()=>trigger.props.onPress({}));let dialog=nodes(tree).find(node=>node.type==='Dialog');const originalReturn=dialog.props.returnFocusRef;
  const list=nodes(tree).find(node=>node.type==='FlatList'),target=list.props.data.find((row:any)=>row.value===policy.recordingOwnerKey(owner('p150').owner));assert(target);
  const row=list.props.renderItem({item:target,index:list.props.data.indexOf(target)});await act(()=>row.props.onPress());await rerender();
  assert.equal(f.select().props.value,target.value);await act(()=>nodes(tree).find(node=>node.type==='Focusable').props.onPress({}));
  dialog=nodes(tree).find(node=>node.type==='Dialog');const originalSelected=dialog.props.initialFocusRef;
  await act(()=>dialog.props.headerContext.props.onPress());assert.deepEqual(f.calls,['','c1']);
  await act(()=>f.pending.resolve({owners:[owner('p201')],grants:f.policy.grants,nextCursor:''}));await rerender();
  dialog=nodes(tree).find(node=>node.type==='Dialog');assert.equal(dialog.props.open,true);assert.equal(dialog.props.returnFocusRef,originalReturn);assert.equal(originalReturn.current,nativeOpener);assert.equal(dialog.props.initialFocusRef,originalSelected);
  assert.equal(f.select().props.value,target.value);const appended=nodes(tree).find(node=>node.type==='FlatList');assert(appended.props.data.some((option:any)=>option.value===policy.recordingOwnerKey(owner('p201').owner)));
  const selectedRow=appended.props.renderItem({item:target,index:appended.props.data.findIndex((option:any)=>option.value===target.value)});assert.equal(selectedRow.props.accessibilityState.checked,true);
 }finally{await innerHost.unmount();await outerHost.unmount();await f.host.unmount();}
});

async function actualControllerGuardFixture(sheet: boolean) {
  const {NativeRecordingOwner, NewRuleSheet} = await loadPresenter();
  const {NativeLiveOwnerController} = await import('../src-next/screens/settings-live/live-owner-controller');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime');
  const lifetime = new NativeScreenLifetime({authority: () => ({serverId: 'server', accountId: 'a', profileId: 'p1', epoch: 'e', viewerFence: 'v'}), current: () => true, subscribeAuthority: () => () => {}});
  const requests: Array<{path: string; method?: string}> = [];
  const controller = new NativeLiveOwnerController(lifetime, {request: async <T,>(path: string, method?: string) => {requests.push({path, method}); return {} as T;}}, () => 'original-remove');
  const choice = owner('p1'), offeredPolicy = {owners: [choice], grants: [{owner: choice.owner, enabled: true, revision: 1}], nextCursor: ''};
  // Only read data is supplied here. The controller's real current/dispose,
  // borrowed lifetime and DELETE command remain in use.
  Object.assign(controller, {tuners: async () => ({sources: [], upcomingWindowHours: 0}), policy: async () => offeredPolicy,
    rules: async () => ({items: [ruleRow(1)], nextCursor: ''}), dvrSettings: async () => ({revision: 1, settings: {}, enumerations: {}}),
    load: async () => {}, getSnapshot: () => ({list: {sources: []}}), guideSeries: async () => []});
  let disposeOnNavigation = false, refuseReads = false, navigationReads = 0, runtimeReads = 0, closes = 0;
  const runtime = {navigation: {getSnapshot: () => {navigationReads++; if (refuseReads) throw Error('retired navigation read'); if (disposeOnNavigation) controller.dispose(); return {revision: 7};}},
    getSnapshot: () => {runtimeReads++; if (refuseReads) throw Error('retired runtime read'); return {contentVisible: true};}, text: {t: (key: string) => key}, error: () => ({})};
  let tree: any;
  // One recordings controller, as the live route supplies it: a fresh identity each render would
  // re-admit the owner page, which is not what this fixture is proving.
  const recordings = {storage: async () => ({policy: {}, measurement: {usedBytes: 0, freeBytes: 0, reservedBytes: 0, writeHealthy: true}, forecastHours: 0})};
  const host = createFabricHookHost(() => {tree = sheet ? NewRuleSheet({controller, runtime, policy: offeredPolicy, sources: [], open: true, onOpenChange: () => {closes++;}, onSaved: () => {}})
    : NativeRecordingOwner({controller, runtime, recordings, sources: []});});
  await act(() => host.mount());
  return {host, controller, lifetime, requests, tree: () => tree, find: (id: string) => nodes(tree).find(node => node.props.testID === id),
    counts: () => ({navigationReads, runtimeReads, closes}), disposeOnNavigation: () => {disposeOnNavigation = true;}, refuseReads: () => {refuseReads = true;},
    dispose: async () => {await host.unmount(); controller.dispose(); lifetime.dispose();}};
}

for (const sheet of [false, true]) test(`actual ${sheet ? 'sheet' : 'page'} navigation getter disposal refuses before the next runtime read`, async () => {
  const f = await actualControllerGuardFixture(sheet);
  try {
    const callback = sheet ? () => f.find('live-owner-new-rule-sheet').props.onOpenChange(false) : f.find('live-owner-new-rule').props.onPress;
    const before = f.counts(); f.disposeOnNavigation();
    await act(callback);
    assert(f.counts().navigationReads > before.navigationReads);
    assert.equal(f.counts().runtimeReads, before.runtimeReads);
    assert.equal(f.counts().closes, 0); assert.equal(f.requests.length, 0);
    assert.equal(f.controller.current(), false); assert.equal(f.lifetime.current(), true);
    if (!sheet) assert.equal(nodes(f.tree()).find(node => node.props.onSaved)?.props.open, false);
  } finally {await f.dispose();}
});

for (const retired of [true, false]) test(`actual removal confirmation ${retired ? 'refuses guarded reads after original disposal' : 'dispatches for its live original controller'}`, async () => {
  const f = await actualControllerGuardFixture(false);
  try {
    await act(() => f.find('live-owner-rule-r1').props.onPress());
    await act(() => f.find('live-owner-rule-menu').props.items.find((item: any) => item.id === 'remove').onSelect());
    const dialog = f.find('live-owner-rule-remove'); assert.equal(dialog.props.open, true);
    const press = nodes(dialog.props.footer).filter(node => node.type === 'Button').at(-1).props.onPress;
    const before = f.counts();
    if (retired) {f.controller.dispose(); f.refuseReads();}
    await act(press);
    assert.equal(f.lifetime.current(), true);
    if (retired) {assert.deepEqual(f.counts(), before); assert.equal(f.requests.length, 0);}
    else {assert.equal(f.requests.length, 1); assert.equal(f.requests[0].method, 'DELETE'); assert.match(f.requests[0].path, /r1\?expectedRevision=1&operationId=original-remove$/);}
  } finally {await f.dispose();}
});
