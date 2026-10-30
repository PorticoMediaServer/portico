import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer,tree,all} from './helpers/interaction-harness.mjs';
import * as tableModel from '../src/kit/settings/table-model.ts';
import {FreshnessHub,dependsOn} from '@core/freshness-hub.ts';
import {mountQuietInput,SettingsOpenContext} from '../src/screens/settings/quiet-refresh.ts';
import {peopleQuietInputs} from '../src/screens/settings/people-refresh.ts';
import {quietClock,deferred,tick} from './settings-quiet-clock.ts';

const row=(accountId:string)=>({accountId,username:accountId,role:'member',revision:1,limitsRevision:1});
const items=Array.from({length:40},(_,i)=>row(i===0?'person-A':i===1?'person-B':`person-${i}`));
const page=(rows=items,cursor='old-cursor')=>({items:rows,nextCursor:cursor,totalCount:100,matchedCount:100,limit:40});
const fresh=()=>page([row('fresh'),...items.slice(1)],'fresh-cursor');
const person=(id:string)=>({accountId:id,username:id,role:'member',revision:1,limitsRevision:1,allowedLibraries:[],limits:{allowRemote:true,conversion:'original',schedule:{windows:[],timezone:'UTC'},maxContentRating:'',allowUnrated:true,accessTokens:true,tagPolicy:{deniedLabels:[]}}});
const turn=()=>new Promise<void>(resolve=>setTimeout(resolve,0));
async function fixture(t:import('node:test').TestContext){
 const c=quietClock(),hub=new FreshnessHub(c.env),h=createRenderer({element:{scrollTop:0,clientHeight:440,querySelectorAll:()=>[]}});
 const resize=Object.getOwnPropertyDescriptor(globalThis,'ResizeObserver');Object.defineProperty(globalThis,'ResizeObserver',{configurable:true,value:class{observe(){}disconnect(){}}});
 const react={...h.react,useId:()=> 'people-table',useImperativeHandle:(ref:any,make:()=>unknown)=>{if(ref)ref.current=make();}};react.default=react;
 const calls:{query:any;cursor:string;signal?:AbortSignal}[]=[],details:{id:string;signal?:AbortSignal}[]=[],outcomes:{id:string;kind:string}[]=[],interests:any[]=[];
 const quietRead=deferred<any>(),tailRead=deferred<any>();let quietCalls=0,table:any;
 let detail:(id:string,signal?:AbortSignal)=>Promise<any>=async id=>person(id);
 const client={read:async(query:any,cursor:string,signal?:AbortSignal)=>{calls.push({query,cursor,signal});return cursor?tailRead.promise:page(query.role==='admin'?[row('admin-result')]:items);},refresh:async()=>++quietCalls===1?quietRead.promise:fresh(),person:async(id:string,signal?:AbortSignal)=>{details.push({id,signal});return detail(id,signal);},dispose(){}};
 const tableModule=await componentModule(new URL('../src/kit/settings/SettingsTable.tsx',import.meta.url),{react,'@i18n':{defaultI18n:{t:(key:string)=>key}},
  '../controls':{Button:'Button',Select:'Select',TextField:'TextField'},'../Icon':{Icon:'Icon'},'../artwork/Artwork':{Artwork:'Artwork'},'./table-model':tableModel,'./settings.module.css':{default:{}}});
 function MountedTable(props:any){table=props;return react.createElement(tableModule.SettingsTable,props);}
 const kit={SettingsTable:MountedTable,SettingsGroup:'SettingsGroup',SettingsPanel:'SettingsPanel',SettingsRows:'SettingsRows',SettingsPageFailure:'SettingsPageFailure',MemberResetCodeSheet:'MemberResetCodeSheet'};
 const app=await componentModule(new URL('../src/screens/settings/People.tsx',import.meta.url),{react,'@i18n':{defaultI18n:{t:(key:string)=>key}},
  '../../platform/freshness':{useFreshness:()=>({hub})},'./quiet-refresh':{SettingsOpenContext,mountQuietInput:(...args:any[])=>{const input=args[3],observed={...input,refresh:async()=>{const result=await input.refresh();outcomes.push({id:input.id,kind:result.kind});return result;}};const owned=mountQuietInput(args[0],args[1],args[2],observed,args[4]);interests.push({id:input.id,...owned});return owned;}},
  './people-refresh':{peopleQuietInputs},'../../kit/settings':kit,'../../kit/controls':{Button:'Button',RichSelect:'RichSelect'},'../../kit/overlays/Dialog':{Dialog:'Dialog'},
  '../../kit/shell/SettingsSurfaces':{SettingsRow:'Row'},'../../kit/accounts':{InvitationShortCodeDialog:'InvitationShortCodeDialog'},
  './people-table-row':{peopleTableRow:(value:any)=>({id:value.accountId,label:value.username,cells:{name:value.username}})},
  './member-reset-code-view':{useMemberResetCodeView:()=>undefined}});
 const bindings={client,actor:{accountId:'owner',role:'owner'},serverName:'Server',libraries:[],authority:{scope:{serverId:'server',authority:'local',accountId:'owner',profileId:'main'},isCurrent:()=>true,subscribe:()=>()=>{},onAuthorityChanged:()=>{}},openHome:()=>{},reportError:(error:unknown)=>{throw error;}};
 const render=()=>tree(h.render(()=>react.createElement(app.PeopleScreen,{bindings})));
 const scroll=()=>{const host=all(render(),'div').find((node:any)=>node.props.onScroll);assert(host);host.props.onScroll({currentTarget:{scrollTop:10000,clientHeight:440}});return render();};
 t.after(()=>{h.dispose();hub.dispose();if(resize)Object.defineProperty(globalThis,'ResizeObserver',resize);else Reflect.deleteProperty(globalThis,'ResizeObserver');});
 render();await turn();await tick();render();
 return {c,hub,render,scroll,table:()=>table,calls,details,outcomes,interests,quietRead,tailRead,quietCalls:()=>quietCalls,detail:(fn:typeof detail)=>{detail=fn;}};
}

for(const order of ['quiet-first','paging-first'] as const)test(`actual table passive paging fences delayed quiet before any next commit: ${order}`,async t=>{
 const f=await fixture(t);const selected=new Set(['person-B']);f.table().onSelectionChange(selected);f.render();
 f.hub.note(dependsOn.list('members'));await f.c.advance(400);assert.equal(f.quietCalls(),1);
 f.scroll();assert.equal(f.calls.at(-1)?.cursor,'old-cursor');const oldRendered=f.table();assert.equal(oldRendered.loading,false,'the passive effect has queued loading without another render');
 const resolveQuiet=async()=>{f.quietRead.resolve(fresh());await tick();};
 const resolvePage=async()=>{f.tailRead.resolve(page([row('loaded-tail')],'tail-cursor'));await tick();};
 if(order==='quiet-first'){await resolveQuiet();await f.c.advance(400);assert.equal(f.quietCalls(),1);await resolvePage();}else{await resolvePage();await resolveQuiet();}
 assert.equal(f.table(),oldRendered,'both responses ran before the next React commit');assert(f.outcomes.some(value=>value.id==='query'&&value.kind==='deferred'));
 await f.c.advance(400);assert.equal(f.quietCalls(),2);await f.c.advance(10000);assert.equal(f.quietCalls(),2,'one gathered owed read, no retry loop');
 f.render();assert.equal(f.table().rows[0].id,'fresh');assert(f.table().rows.some((value:any)=>value.id==='loaded-tail'));assert.equal(f.table().selected,selected);
 // The next old/new table callback must use the fresh first-page continuation.
 f.table().onLoadMore();assert.equal(f.calls.at(-1)?.cursor,'fresh-cursor');
});

test('a quiet page installed before redraw supplies the cursor/page to the old rendered More callback',async t=>{
 const f=await fixture(t),old=f.table(),more=old.onLoadMore;
 f.hub.note(dependsOn.list('members'));await f.c.advance(400);f.quietRead.resolve(fresh());await tick();assert(f.outcomes.some(value=>value.id==='query'&&value.kind==='adopted'));assert.equal(f.table(),old);
 more();assert.equal(f.calls.at(-1)?.cursor,'fresh-cursor');assert.equal(f.table(),old);f.tailRead.resolve(page([row('loaded-tail')],'fresh-chain-tail'));await tick();f.render();
 assert.equal(f.table().rows[0].id,'fresh');assert(f.table().rows.some((value:any)=>value.id==='loaded-tail'));assert.equal(f.quietCalls(),1);
});

test('query admission rejects old quiet and paging responses synchronously before a new query commit',async t=>{
 const f=await fixture(t);f.hub.note(dependsOn.list('members'));await f.c.advance(400);f.scroll();const old=f.table(),paging=f.calls.at(-1)!;
 old.onQueryChange({...old.query,filters:{...old.query.filters,role:'admin'}});assert.equal(paging.signal?.aborted,true);
 f.quietRead.resolve(fresh());f.tailRead.resolve(page([row('stale-tail')],'stale-tail-cursor'));await tick();assert.equal(f.table(),old);assert(f.outcomes.some(value=>value.id==='query'&&value.kind==='deferred'));
 f.render();await turn();await tick();f.render();assert.equal(f.table().rows[0].id,'admin-result');assert(!f.table().rows.some((value:any)=>value.id==='stale-tail'));assert.equal(f.calls.at(-1)?.query.role,'admin');
});

test('selection admission fences old ordinary and quiet exact-detail responses before the selection commit',async t=>{
 const f=await fixture(t),ordinary=deferred<any>(),quiet=deferred<any>();let a=0;f.detail(async id=>id==='person-A'?(++a===1?ordinary.promise:quiet.promise):person(id));
 f.table().rowAction.onAction({id:'person-A'},null);f.render();assert.equal(f.details.length,1);
 f.interests.find(value=>value.id==='selected-detail').invalidate();await f.c.advance(400);assert.equal(f.details.length,2);const old=f.table();old.rowAction.onAction({id:'person-B'},null);assert.equal(f.details[0].signal?.aborted,true);
 ordinary.resolve(person('person-A'));quiet.resolve(person('person-A'));await tick();assert.equal(f.table(),old);assert(f.outcomes.some(value=>value.id==='selected-detail'&&value.kind==='deferred'));
 f.render();await tick();const nodes=f.render();const panel=all(nodes,'SettingsPanel').find((node:any)=>node.props.open);assert.equal(panel?.props.title,'person-B');assert.equal(f.details.at(-1)?.id,'person-B');
});

test('opening and switching a person preserves an unrelated ordinary page and its next automatic demand',async t=>{
 const f=await fixture(t);f.scroll();assert.equal(f.calls.at(-1)?.cursor,'old-cursor');
 f.table().rowAction.onAction({id:'person-A'},null);f.render();await tick();f.table().rowAction.onAction({id:'person-B'},null);f.render();await tick();
 f.tailRead.resolve(page([row('loaded-tail')],'tail-cursor'));await tick();f.render();
 assert(f.table().rows.some((value:any)=>value.id==='loaded-tail'),'detail selection cannot consume the table page');
 f.scroll();await tick();f.render();assert(f.calls.some(value=>value.cursor==='tail-cursor'),'normal scrolling reaches the installed continuation');assert.equal(f.quietCalls(),0);
});

for(const order of ['ordinary-first','quiet-first'] as const)test(`ordinary and quiet detail keep the newest accepted detail: ${order}`,async t=>{
 const f=await fixture(t),ordinary=deferred<any>(),quiet=deferred<any>();let reads=0;
 f.detail(async()=>++reads===1?ordinary.promise:quiet.promise);
 f.table().rowAction.onAction({id:'person-A'},null);f.render();
 f.interests.find(value=>value.id==='selected-detail').invalidate();await f.c.advance(400);assert.equal(f.details.length,2);
 const settleOrdinary=async()=>{ordinary.resolve({...person('person-A'),username:'old-name',revision:1});await tick();f.render();};
 const settleQuiet=async()=>{quiet.resolve({...person('person-A'),username:'fresh-name',revision:2});await tick();f.render();};
 if(order==='ordinary-first'){await settleOrdinary();await settleQuiet();}else{await settleQuiet();await settleOrdinary();}
 const panel=all(f.render(),'SettingsPanel').find((node:any)=>node.props.open);assert.equal(panel?.props.title,'fresh-name');
 assert(f.outcomes.some(value=>value.id==='selected-detail'&&value.kind==='adopted'));
});
