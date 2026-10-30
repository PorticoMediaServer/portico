import test from 'node:test';
import assert from 'node:assert/strict';
import {FreshnessHub,dependsOn} from '@core/freshness-hub.ts';
import type {SettingsPeopleClient,SettingsPeoplePage,SettingsPersonRow} from '@core/settings-people.ts';
import type {AccessMember} from '@core/server-administration.ts';
import {peopleQuietInputs,revalidatedPeoplePage,type PeopleRefreshSnapshot} from '../src/screens/settings/people-refresh.ts';
import {mountQuietInput} from '../src/screens/settings/quiet-refresh.ts';
import {quietClock,deferred,tick} from './settings-quiet-clock.ts';
const row=(accountId:string,revision=1)=>({accountId,username:accountId,revision} as SettingsPersonRow);
const page=(items:readonly SettingsPersonRow[],nextCursor='new-first-cursor'):SettingsPeoplePage=>({items,nextCursor,totalCount:100,matchedCount:100,limit:40});
const query={search:'retained search',role:'all',status:'all',sort:'active',direction:'desc'} as const;
function fixture(){
 let current=true,busy=false,serial=0,firstIds:ReadonlySet<string>=new Set(['first']);
 let state:PeopleRefreshSnapshot={page:page([row('first'),row('tail')],'old-tail-cursor'),query,personId:'tail',person:{accountId:'tail',revision:1} as AccessMember};
 let readQuery:unknown,personId:string|undefined,firstReads=0,detailReads=0,closed:string|undefined,denied:unknown;
 let list:()=>Promise<SettingsPeoplePage>=async()=>page([row('first',2)]),detail:()=>Promise<AccessMember>=async()=>({accountId:'tail',revision:2} as AccessMember);
 const listeners=new Set<()=>void>(),requests=new Set<AbortController>();
 const client={refresh:async(q:unknown)=>{firstReads++;readQuery=q;return list();},person:async(id:string)=>{detailReads++;personId=id;return detail();}} as SettingsPeopleClient;
 const inputs=peopleQuietInputs({client,get:()=>state,serial:()=>serial,current:()=>current,blocked:()=>busy,firstPageIds:()=>firstIds,requests,
  subscribe:listener=>{listeners.add(listener);return()=>{listeners.delete(listener);};},installPage:(page,ids)=>{state={...state,page};firstIds=ids;},installPerson:person=>{state={...state,person};},closePerson:id=>{closed=id;state={...state,personId:undefined,person:undefined};},denied:error=>{denied=error;}});
 return {inputs,state:()=>state,counts:()=>[firstReads,detailReads],reads:()=>({query:readQuery,id:personId}),closed:()=>closed,denied:()=>denied,
  list:(fn:typeof list)=>{list=fn;},detail:(fn:typeof detail)=>{detail=fn;},write:()=>{busy=true;serial++;for(const listener of listeners)listener();},settle:()=>{busy=false;for(const listener of listeners)listener();},retire:()=>{current=false;},select:(id:string)=>{serial++;state={...state,personId:id};},requests};
}

test('same-query first page installs its fresh cursor, bounded loaded tail and exact selected tail detail independently',async()=>{
 const f=fixture(),before=f.state();assert.deepEqual(await f.inputs[0].refresh(),{kind:'adopted'});assert.deepEqual(await f.inputs[1].refresh(),{kind:'adopted'});
 assert.deepEqual(f.reads(),{query,id:'tail'});assert.equal(f.state().page?.nextCursor,'new-first-cursor');assert.deepEqual(f.state().page?.items.map(r=>r.accountId),['first','tail']);assert.equal(f.state().page?.items[1],before.page?.items[1]);assert.equal(f.state().person?.revision,2);assert.equal(f.state().personId,'tail');
 let shown=f.state().page!;for(let i=0;i<10;i++)shown=revalidatedPeoplePage(shown,page([row('new-'+i)]),new Set(shown.items.slice(0,1).map(r=>r.accountId)));assert.equal(shown.items.length,2);
});

test('first-page failure leaves selected detail independently owed and adopted; tail absence is not removal',async()=>{
 const c=quietClock(),hub=new FreshnessHub(c.env),f=fixture(),error=Object.assign(new Error('busy'),{retryAfterSeconds:7200});f.list(async()=>{throw error;});
 const interests=f.inputs.map(input=>mountQuietInput(hub,'settings/people',[dependsOn.list('members')],input));hub.note(dependsOn.list('members'));await c.advance(400);
 assert.equal(interests[0].getState().owed,true);assert.equal(interests[1].getState().owed,false);assert.equal(f.state().person?.revision,2);assert.equal(f.state().personId,'tail');assert.equal(f.closed(),undefined);assert.equal(f.state().page?.nextCursor,'old-tail-cursor');
 for(const input of interests)input.dispose();hub.dispose();assert.equal(c.pending(),0);
});

test('only exact selected detail 404 closes that target; 401/403 takes original viewer failure path',async()=>{
 for(const status of [404,401,403]){const f=fixture(),error=Object.assign(new Error('original'),{status});f.detail(async()=>{throw error;});const result=await f.inputs[1].refresh();if(status===404){assert.deepEqual(result,{kind:'adopted'});assert.equal(f.closed(),'tail');}else{assert.equal(result.kind,'unreadable');if(result.kind==='unreadable')assert.equal(result.error,error);assert.equal(f.denied(),error);assert.equal(f.closed(),undefined);}}
});

test('write starts and finishes during reads, selected replacement, hide and retirement prevent late installation',async()=>{
 for(const action of ['write','select','hide','retire'] as const){const f=fixture(),held=deferred<AccessMember>();f.detail(()=>held.promise);const before=f.state(),pending=f.inputs[1].refresh();if(action==='write'){f.write();f.settle();}if(action==='select')f.select('other');if(action==='hide')f.inputs[1].cancel?.();if(action==='retire')f.retire();held.resolve({accountId:'tail',revision:9} as AccessMember);assert.deepEqual(await pending,{kind:'deferred'});if(action!=='select')assert.equal(f.state(),before);assert.equal(f.requests.size,0);}
});
