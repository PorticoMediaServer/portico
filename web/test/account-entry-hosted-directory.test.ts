import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi,type LocalSession} from '@core/index.ts';
import {AppSessionStore,type AppSessionRecord} from '@core/app-sessions.ts';
import {parseDirectSignIn} from '@core/profile-management.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import type {AccountServerAccess} from '../src/platform/account-server-access.ts';
import {browserAppSessions,observeBrowserSessionChanges} from '../src/platform/server-connections.ts';
import {fakeIndexedDB} from './protected-store-fixture.ts';

const origin='https://actual-server.test',serverId='srv_'+'A'.repeat(43),pin={serverId,publicKey:'p'.repeat(43),fingerprint:'f'.repeat(43)};
const hint={id:serverId,name:'Directory name',baseUrl:origin,publicKey:pin.publicKey,policyRevision:3,presence:{lastSeenAt:null}};
function fixture(selectedStore?:AppSessionStore){
  let raw:AppSessionRecord|undefined,live=true,kind='portico',challenge=false,foreign=false,remembers=0,disposed=0;
  let held:Promise<void>|undefined,entered!:()=>void,release!:()=>void,holdAt='';
  const calls:string[]=[],retired:string[]=[],listeners=new Set<()=>void>();
  const store=selectedStore??new AppSessionStore({read:async()=>raw,change:async(update,beforeCommit)=>{const next=update(raw);beforeCommit?.();raw=next;}});
  const session=(family='new-server-family'):LocalSession=>({accessToken:'private-access-'+family,refreshToken:'private-refresh-'+family,sessionFamilyId:family,tokenGeneration:'1',installationId:'i'.repeat(43),deviceId:'actual-device',expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2099-02-01T00:00:00Z',viewer:{authority:'local',serverId,accountId:'server-member',profileId:'main',role:'account'}});
  const snapshot=()=>({authority:'local',serverId,canManage:false,twoStepSetupRequired:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false},
    account:{id:'server-member',username:'actual-member',hostedAccountId:foreign?'foreign-hosted':'actual-hosted',primaryProfileId:'main',role:'member',revision:1,disabled:false,allowedLibraries:[]},
    profiles:[{id:'main',name:'Actual main',primary:true,position:0,art:'mint',revision:1,pinRevision:1,pinRequired:false}]});
  const result=()=>parseDirectSignIn({...snapshot(),session:session()});
  const system=()=>({id:serverId,name:'Verified Server name',version:'1',signIn:kind,move:null,setupRequired:false,hostedConfigured:true,hostedAttached:true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []});
  const connection={pin,logicalOrigin:origin,getSnapshot:()=>({phase:'ready'}),getMemory:()=>({paired:[origin]}),setPersistence:()=>{},dispose:()=>{disposed++;}};
  const hosted={origin:'https://accounts.test',accountId:'actual-hosted',key:{keyId:'actual-key',publicKey:'k'.repeat(43)}};
  const account:AccountServerAccess={origin:hosted.origin,accountId:hosted.accountId,isCurrent:()=>live,observe:listener=>{listeners.add(listener);return()=>{listeners.delete(listener);};},request:async()=>{throw new Error('Injected proof boundary must not issue another Hosted request.');}};
  const pause=async(where:string)=>{if(held&&holdAt===where){entered();await held;}};
  const api=new HttpLocalApi(origin,'');api.getRouteConnection=()=>connection as never;
  api.request=(async(path:string)=>{calls.push(path);if(path==='/v1/system')return system();if(path==='/v1/auth/two-factor/challenge'){await pause('factor');return {...snapshot(),session:session()};}throw new Error('Unexpected request '+path);}) as HttpLocalApi['request'];
  const entry=new BrowserAccountEntry({store,actions:{} as never,environment:()=>({storage:store.connectionStorage(serverId)}),restore:async()=>{throw new Error('Never-saved Server must not restore.');},inspect:async()=>{throw new Error('No unsigned address inspection.');},connect:async()=>{throw new Error('No paired route guess.');},
    connectHosted:(async(server,source)=>{calls.push('signed-routes');assert.equal(server.id,serverId);assert.equal(source.accountId,'actual-hosted');assert.equal('presence' in server,false);await pause('connect');return {api,connection,hosted};}) as never,
    signInHosted:(async()=>{calls.push('sign-in');await pause('sign-in');return challenge?{kind:'challenge',token:'private-challenge',expiresAt:'2099-01-01T00:00:00Z'}:result();}) as never,
    remember:async(...args)=>{remembers++;await rememberNativeSession(...args);},retire:async(_api,value)=>{retired.push(value.sessionFamilyId);},
  });
  return {entry,store,account,calls,retired,result,session,hosted,connection,remembers:()=>remembers,disposed:()=>disposed,
    local:()=>{kind='local';},foreign:()=>{foreign=true;},challenge:()=>{challenge=true;},replace:()=>{live=false;for(const changed of listeners)changed();},
    hold:(where:string)=>{holdAt=where;held=new Promise(resolve=>{release=resolve;});return new Promise<void>(resolve=>{entered=resolve;});},release:()=>{held=undefined;release();},
    successor:async()=>{await store.connectionStorage(serverId,{signIn:'portico',hostedAccount:hosted,expectedRevision:(await store.read()).revision}).change(()=>({version:'1',selectionId:'s'.repeat(32),pin,logicalOrigin:origin,routes:{paired:[origin]},hosted,session:session('successor-family')}));},
  };
}
test('never-saved current Hosted membership uses verified routes and durable account-only admission',async()=>{
  const f=fixture();assert.equal(await f.entry.openHostedServer(hint,f.account),true);
  assert.deepEqual(f.calls,['signed-routes','/v1/system','sign-in']);assert.equal(f.remembers(),1);
  assert.equal(f.entry.getSnapshot().server?.name,'Verified Server name');assert.equal(f.entry.getSnapshot().phase,'profiles');
  const saved=await f.store.selected();assert.equal(saved?.signIn,'portico');assert.equal(saved?.connection.hosted?.accountId,'actual-hosted');assert.equal(saved?.connection.session.viewer.role,'account');f.entry.dispose();
});
test('verified Local kind refuses before any Hosted identity or Server admission',async()=>{
  const f=fixture();f.local();assert.equal(await f.entry.openHostedServer(hint,f.account),false);
  assert.deepEqual(f.calls,['signed-routes','/v1/system']);assert.equal(f.remembers(),0);assert.equal(await f.store.selected(),undefined);f.entry.dispose();
});
test('cancelled proof resolution cannot send credentials or publish a late Server',async()=>{
  const f=fixture(),started=f.hold('connect'),pending=f.entry.openHostedServer(hint,f.account);await started;f.entry.cancel();f.release();await pending;
  assert.deepEqual(f.calls,['signed-routes']);assert.equal(f.remembers(),0);assert.equal(f.entry.getSnapshot().phase,'welcome');assert.ok(f.disposed()>0);f.entry.dispose();
});
test('a late issued grant after family replacement retires only itself and preserves successor revision',async()=>{
  const f=fixture(),started=f.hold('sign-in'),pending=f.entry.openHostedServer(hint,f.account);await started;await f.successor();const revision=(await f.store.read()).revision;f.replace();f.release();await pending;
  assert.deepEqual(f.retired,['new-server-family']);assert.equal(f.remembers(),0);assert.equal((await f.store.read()).revision,revision);assert.equal((await f.store.selected())?.connection.session.sessionFamilyId,'successor-family');f.entry.dispose();
});
test('a different returned Hosted account cannot be admitted',async()=>{
  const f=fixture();f.foreign();assert.equal(await f.entry.openHostedServer(hint,f.account),false);
  assert.deepEqual(f.retired,['new-server-family']);assert.equal(f.remembers(),0);assert.equal(await f.store.selected(),undefined);f.entry.dispose();
});
test('Hosted family retirement clears a private MFA challenge without admitting a profile',async()=>{
  const f=fixture();f.challenge();assert.equal(await f.entry.openHostedServer(hint,f.account),true);assert.equal(f.entry.getSnapshot().phase,'challenge');f.replace();
  assert.equal(f.entry.getSnapshot().phase,'welcome');assert.equal(await f.store.selected(),undefined);await f.entry.completeChallenge('123456');assert.equal(f.calls.includes('/v1/auth/two-factor/challenge'),false);f.entry.dispose();
});
test('MFA completion retains Hosted route metadata and normal account-only profile selection',async()=>{
  const f=fixture();f.challenge();await f.entry.openHostedServer(hint,f.account);await f.entry.completeChallenge('123456');
  assert.equal(f.entry.getSnapshot().phase,'profiles');assert.equal((await f.store.selected())?.connection.hosted?.accountId,'actual-hosted');assert.equal(f.remembers(),1);f.entry.dispose();
});
test('actual encrypted admission cancelled before commit never publishes a record or retirement hint',async t=>{
  const db=fakeIndexedDB(),page=new EventTarget(),sent:unknown[]=[],globals=new Map<string,PropertyDescriptor|undefined>();
  let tail=Promise.resolve();
  class Channel extends EventTarget{postMessage(value:unknown){sent.push(value);}close(){}}
  const replacements={indexedDB:db.indexedDB,window:page,BroadcastChannel:Channel,navigator:{locks:{request:(_name:string,_options:unknown,work:()=>Promise<unknown>)=>{
    const result=tail.then(work);tail=result.then(()=>{},()=>{});return result;
  }}}};
  for(const [name,value]of Object.entries(replacements)){globals.set(name,Object.getOwnPropertyDescriptor(globalThis,name));Object.defineProperty(globalThis,name,{configurable:true,value});}
  const store=browserAppSessions(),f=fixture(store);let heard=0;
  const stop=observeBrowserSessionChanges(()=>{heard++;});
  t.after(()=>{f.entry.dispose();stop();for(const [name,descriptor]of globals){if(descriptor)Object.defineProperty(globalThis,name,descriptor);else delete(globalThis as Record<string,unknown>)[name];}});
  let release!:()=>void,entered!:()=>void;
  const waiting=new Promise<void>(resolve=>{release=resolve;}),started=new Promise<void>(resolve=>{entered=resolve;});
  const encrypt=crypto.subtle.encrypt.bind(crypto.subtle);
  t.mock.method(crypto.subtle,'encrypt',async(...args:Parameters<typeof encrypt>)=>{const cipher=await encrypt(...args);entered();await waiting;return cipher;});
  const pending=f.entry.openHostedServer(hint,f.account);await started;
  assert.equal(db.data.has('selected'),false);assert.equal(heard,0);assert.deepEqual(sent,[]);
  f.replace();f.entry.cancel();const successor=f.successor();
  release();assert.equal(await pending,false);await successor;
  assert.deepEqual(f.retired,['new-server-family']);
  assert.equal((await store.selected())?.connection.session.sessionFamilyId,'successor-family');
  assert.equal((await store.read()).revision,1,'only the real successor committed; no cancelled admission or rollback');
  assert.equal(heard,1,'only the successor emitted a same-tab hint');
  assert.deepEqual(sent,['changed'],'only the successor emitted a cross-tab hint');
});
