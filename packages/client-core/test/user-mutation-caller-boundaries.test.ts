import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, emptyUserMutationState, type LocalSession, type UserMutationState} from '../src/index.ts';
import {DraftForm} from '../src/server-admin/draft-form.ts';
import {saveLookup, type ScreenLookupPolicy} from '../src/server-admin/metadata-screen.ts';
import {userMutationCredentialURL} from '../src/user-mutations.ts';
import {routePath} from '../../contracts/src/route-paths.ts';

for(const bounded of [false,true])test('a retained settings conflict cannot be automatically rebased through '+(bounded?'console':'ordinary')+' transport',async()=>{
 let state=emptyUserMutationState(),writes=0,reads=0,ids=0;
 const clone=<T>(v:T):T=>JSON.parse(JSON.stringify(v));
 const store={read:async()=>clone(state),compareAndSwap:async(revision:number,next:UserMutationState)=>{if(revision!==state.revision)return false;state=clone(next);return true;}};
 const session={accessToken:'test',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}} as LocalSession;
 const api=new HttpLocalApi('http://server.test','test',async(input,init)=>{
  const path=new URL(String(input)).pathname;
  if(path==='/v1/user-mutations/policy')return Response.json({serverTimeMs:Date.now()});
  if(path.startsWith('/v1/user-mutations/'))return Response.json({error:{code:'receipt_unknown'}},{status:404});
  writes++;return Response.json({error:{code:'console_conflict',currentRevision:8}},{status:409});
 });
 api.setUserMutationSession(session);const coordinator=api.configureUserMutations(store);
 const form=new DraftForm({load:async()=>{reads++;return {value:{revision:7,value:'before'}};},save:async(draft:{revision:number;value:string},base:{revision:number;value:string})=>{
  const body={expectedRevision:base.revision,idempotencyKey:'caller-'+(++ids),values:{value:draft.value}};
  if(bounded)await api.requestConsole(routePath('change_console_settings'),'PATCH',body,new AbortController().signal);
  else await api.request(routePath('change_console_settings'),'PATCH',body);
 }});
 try{
  await form.load();form.set({value:'authored'});await assert.rejects(form.save(),(error:unknown)=>!!error&&typeof error==='object'&&/^p251_/.test(String((error as {mutationKey?:string}).mutationKey)));
  assert.equal(writes,1);assert.equal(reads,1);assert.equal(ids,1);assert.equal(form.getSnapshot().saved?.revision,7);assert.equal(form.getSnapshot().draft?.value,'authored');
  assert.equal(state.entries.length,1);assert.equal(state.entries[0].state,'review');assert.equal((state.entries[0].body as {expectedRevision:number}).expectedRevision,7);
  await assert.rejects(form.save());assert.equal(writes,1,'a replacement caller ID cannot bypass retained conflict review');assert.equal(state.entries.length,1);
 }finally{form.dispose();coordinator.dispose();}
});

test('metadata screen conflict preserves the original request without a fresh read or revision',async()=>{
 const sent:{path:string;method?:string;body?:unknown}[]=[];
 const policy={revision:7,consentRevision:2,enabled:true,confirmed:true,status:'enabled',providers:['tmdb'],language:'en',region:'',refreshMode:'missing'} as ScreenLookupPolicy;
 await assert.rejects(saveLookup(async(path,method,body)=>{sent.push({path,method,body});throw {status:409,code:'metadata_conflict'};},'library',policy,false));
 assert.equal(sent.length,1);assert.equal(sent[0].method,'PUT');assert.equal((sent[0].body as {expectedRevision:number}).expectedRevision,7);
});

test('credential URL scanner handles bounded plain text and separated URL forms',()=>{
 assert.equal(userMutationCredentialURL('x'.repeat(65536)),false);
 assert.equal(userMutationCredentialURL('x'.repeat(32768)+' https://guide.test/list?%74oken=private'),true);
 assert.equal(userMutationCredentialURL('https://public.test/list https://user:private@guide.test/list'),true);
 assert.equal(userMutationCredentialURL('https://public.test/list?language=en'),false);
});
