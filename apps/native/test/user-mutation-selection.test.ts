import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeUserMutationSelectionOwner} from '../src-next/platform/user-mutation-selection.ts';
import {sameUserMutationScope,type UserMutationScope} from '../../../packages/client-core/src/user-mutations.ts';
const original:UserMutationScope={serverId:'server',origin:'http://192.0.2.1',authority:'local',accountId:'account',profileId:'profile',familyId:'family',deviceId:'device'};
function fixture(){
 let selected:UserMutationScope|undefined=original,active:UserMutationScope|undefined;let state='pending',checks=Promise.resolve();const errors:unknown[]=[];
 const listeners=new Set<()=>void>();
 const store={activate:async(scope:UserMutationScope)=>{if(active&&!sameUserMutationScope(active,scope))state='quarantined';active=scope;},retire:async(scope:UserMutationScope)=>{if(!active||!sameUserMutationScope(active,scope))return false;active=undefined;state='quarantined';return true;}};
 const owner=new NativeUserMutationSelectionOwner(work=>{const run=checks.then(()=>work(selected));checks=run.then(()=>{},()=>{});return run;},listener=>{listeners.add(listener);});
 return {store,owner,errors,state:()=>state,active:()=>active,select:(scope:UserMutationScope|undefined)=>{selected=scope;for(const listener of listeners)listener();},flush:()=>checks};
}
test('same-family renewal and shell mount replacement preserve pending custody',async()=>{
 const f=fixture();await f.owner.activate(f.store,original,error=>f.errors.push(error));
 // Token generation/horizon changed, but the exact selected mutation scope did not.
 f.select({...original});await f.flush();await f.owner.activate(f.store,{...original},error=>f.errors.push(error));
 assert.equal(f.state(),'pending');assert.deepEqual(f.active(),original);assert.deepEqual(f.errors,[]);
});
test('actual account/profile/family/server/origin/device switch or explicit logout retires old scope',async()=>{
 for(const changed of [undefined,...(['accountId','profileId','familyId','serverId','origin','deviceId'] as const).map(key=>({...original,[key]:original[key]+'-changed'}))]){
  const f=fixture();await f.owner.activate(f.store,original,error=>f.errors.push(error));f.select(changed);await f.flush();
  assert.equal(f.state(),'quarantined');assert.equal(f.active(),undefined);
 }
});
test('a stale selection observer cannot activate an old mount over a new family',async()=>{
 const f=fixture();await f.owner.activate(f.store,original,error=>f.errors.push(error));
 const successor={...original,familyId:'successor'};f.select(successor);await f.owner.activate(f.store,successor,error=>f.errors.push(error));await f.flush();
 await assert.rejects(f.owner.activate(f.store,original,error=>f.errors.push(error)),{code:'mutation_scope_changed'});
 assert.deepEqual(f.active(),successor);
});
