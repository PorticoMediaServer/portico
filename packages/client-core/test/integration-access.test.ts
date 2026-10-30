import assert from 'node:assert/strict';
import test from 'node:test';
import type {ApiAccessTransport} from '../src/api-access.ts';
import {SettingsIntegrationsSource} from '../src/server-admin/integrations-source.ts';
import {SettingsApiKeysSource} from '../src/server-admin/api-keys-source.ts';
const options={scopes:[{id:'read'},{id:'playback'},{id:'library'},{id:'admin'}],maximumKeys:100,maximumTokens:25,tokensAllowed:true,requestsPerMinute:600,queryParameter:'apiKey'};
const app={id:'app',name:'Home automation',username:'sam',accountId:'account',profileId:'profile',scopes:['read','playback'],state:'active'};
const user={accountId:'account',profileId:'profile',profileName:'Sam',username:'sam',role:'owner',disabled:false};
const key={id:'key',kind:'key',name:'Automation',scopes:['read'],hint:'',createdAt:'2026-10-28T12:00:00Z',state:'active',member:{accountId:'account',profileId:'profile',username:'sam',profile:'Sam'}};
const transport=(run:(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>unknown):ApiAccessTransport=>({request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>Promise.resolve(run(path,method,body,signal)) as Promise<T>});
function reads(path:string){if(path.startsWith('/v1/integrations/users'))return {items:[user]};if(path==='/v1/admin/api-access')return options;if(path.includes('api-access'))return options;return {items:[app]};}

test('app-password create retries one exact grant intent and keeps the secret out of snapshots',async()=>{
 const bodies:unknown[]=[];let first=true;
 const api=transport((path,method,body)=>{
  if(method==='POST'){bodies.push(body);if(first){first=false;return Promise.reject(new Error('response lost'));}return {appPassword:app,secret:'papp.private'};}
  return reads(path);
 });
 const source=new SettingsIntegrationsSource(api,()=>true,()=>{},()=> 'operation-fixed');
 await source.load();assert.equal(source.getSnapshot().phase,'ready');
 assert.equal(await source.create({name:app.name,accountId:app.accountId,profileId:app.profileId,scopes:['read','playback']}),undefined);
 assert.equal(source.getSnapshot().pendingCreate,true);
 const result=await source.retryCreate();assert.equal(result?.secret,'papp.private');assert.deepEqual(bodies[0],bodies[1]);
 assert.equal(JSON.stringify(source.getSnapshot()).includes('papp.private'),false);
 source.dispose();
});
test('app-password reveals are discarded after owner/server retirement and never enter shared state',async()=>{
 let resolve:(v:unknown)=>void=()=>{};let signal:AbortSignal|undefined;
 const api=transport((path,_method,_body,s)=>path.endsWith('/secret')?(signal=s,new Promise(done=>{resolve=done;})):reads(path));
 const source=new SettingsIntegrationsSource(api,()=>true,()=>{});await source.load();const pending=source.reveal(source.getSnapshot().apps!.items[0]);
 source.dispose();assert.equal(signal?.aborted,true);resolve({id:'app',secret:'papp.private'});assert.equal(await pending,undefined);assert.equal(source.getSnapshot().phase,'idle');
});
test('API key recovery returns only to the current explicit caller and is canceled on a changed listing',async()=>{
 let resolve:(v:unknown)=>void=()=>{};
 const api=transport(path=>path.endsWith('/secret')?new Promise(done=>{resolve=done;}):path.includes('api-access')?options:{items:[key],limit:100,total:1});
 const source=new SettingsApiKeysSource(api,()=>true,()=>{});await source.load();const selected=source.getSnapshot().page!.items[0],pending=source.reveal(selected);
 await source.load();resolve({id:'key',secret:'pk.private'});assert.equal(await pending,undefined);assert.equal(JSON.stringify(source.getSnapshot()).includes('pk.private'),false);source.dispose();
});
test('app credentials cannot target an absent profile or acquire an unoffered scope',async()=>{
 let posts=0;const source=new SettingsIntegrationsSource(transport((path,method)=>{if(method==='POST')posts++;return reads(path);}),()=>true,()=>{});await source.load();
 assert.equal(await source.create({name:'Wrong profile',accountId:'account',profileId:'other',scopes:['admin']}),undefined);
 assert.equal(await source.create({name:'Conflicting scope',accountId:'account',profileId:'profile',scopes:['admin','read']}),undefined);
 assert.equal(posts,0);source.dispose();
});
