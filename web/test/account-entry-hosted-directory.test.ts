import test from 'node:test';
import {systemAnswer} from '../../packages/client-core/test/next-wire.ts';
import assert from 'node:assert/strict';
import {HttpLocalApi,type LocalSession} from '@core/index.ts';
import {AppSessionStore,type AppSessionRecord} from '@core/app-sessions.ts';
import {parseDirectSignIn} from '@core/profile-management.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import type {PorticoServerAccess} from '../src/platform/portico-server-access.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import {browserAppSessions,observeBrowserSessionChanges} from '../src/platform/server-connections.ts';
import {fakeIndexedDB} from './protected-store-fixture.ts';

const origin='https://actual-server.test',serverId='srv_'+'A'.repeat(43),pin={serverId,publicKey:'p'.repeat(43),fingerprint:'f'.repeat(43)};
const hint={id:serverId,name:'Directory name',baseUrl:origin,publicKey:pin.publicKey};
test.beforeEach(()=>setInstallationIdentity({installationId:'i'.repeat(43),name:'Test browser',platform:'web',app:'portico',appVersion:'1'}));
test.after(()=>setInstallationIdentity(undefined));
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
  // Remembered under its Portico Account, with no route key (the new service keeps none).
  const hosted={origin:'https://accounts.test',accountId:'actual-hosted'};
  const note=()=>({identity:{payload:btoa(JSON.stringify({kind:'portico.identity.v1',accountId:'actual-hosted',serverId,challenge:'c'.repeat(43)})).replace(/=+$/,'').replace(/\+/g,'-').replace(/\//g,'_'),signature:'s',keyId:'k'}});
  const account:PorticoServerAccess={origin:hosted.origin,accountId:hosted.accountId,isCurrent:()=>live,observe:listener=>{listeners.add(listener);return()=>{listeners.delete(listener);};},identity:async()=>{calls.push('note');return note();}};
  const pause=async(where:string)=>{if(held&&holdAt===where){entered();await held;}};
  const api=new HttpLocalApi(origin,'');api.getRouteConnection=()=>connection as never;
  api.request=(async(path:string)=>{calls.push(path);if (path === '/v1/system') return systemAnswer(system());
   if(path==='/v1/auth/portico/challenge')return {challenge:'c'.repeat(43),serverId,expiresAt:'2099-01-01T00:00:00Z'};
   if(path==='/v1/auth/portico/exchange'){await pause('sign-in');return challenge?{challenge:{token:'private-challenge',expiresAt:'2099-01-01T00:00:00Z'}}:{...snapshot(),session:session()};}
   if(path==='/v1/auth/two-factor'){await pause('factor');return {...snapshot(),session:session()};}throw new Error('Unexpected request '+path);}) as HttpLocalApi['request'];
  const entry=new BrowserAccountEntry({store,actions:{} as never,environment:()=>({storage:store.connectionStorage(serverId)}),restore:async()=>{throw new Error('Never-saved Server must not restore.');},inspect:(async(address:string)=>{calls.push('prove-key');assert.equal(address,origin);return pin;}) as never,connect:(async()=>{calls.push('connect');await pause('connect');return {api,connection};}) as never,
    remember:async(...args)=>{remembers++;await rememberNativeSession(...args);},retire:async(_api,value)=>{retired.push(value.sessionFamilyId);},
  });
  return {entry,store,account,calls,retired,result,session,hosted,connection,remembers:()=>remembers,disposed:()=>disposed,
    local:()=>{kind='local';},foreign:()=>{foreign=true;},challenge:()=>{challenge=true;},replace:()=>{live=false;for(const changed of listeners)changed();},
    hold:(where:string)=>{holdAt=where;held=new Promise(resolve=>{release=resolve;});return new Promise<void>(resolve=>{entered=resolve;});},release:()=>{held=undefined;release();},
    successor:async()=>{await store.connectionStorage(serverId,{signIn:'portico',hostedAccount:hosted,expectedRevision:(await store.read()).revision}).change(()=>({version:'1',selectionId:'s'.repeat(32),pin,logicalOrigin:origin,routes:{paired:[origin]},hosted,session:session('successor-family')}));},
  };
}
test('verified Local kind refuses before any Hosted identity or Server admission',async()=>{
  const f=fixture();f.local();assert.equal(await f.entry.openHostedServer(hint,f.account),false);
  assert.deepEqual(f.calls,['prove-key','connect','/v1/system']);assert.equal(f.remembers(),0);assert.equal(await f.store.selected(),undefined);f.entry.dispose();
});
test('cancelled proof resolution cannot send credentials or publish a late Server',async()=>{
  const f=fixture(),started=f.hold('connect'),pending=f.entry.openHostedServer(hint,f.account);await started;f.entry.cancel();f.release();await pending;
  assert.deepEqual(f.calls,['prove-key','connect']);assert.equal(f.remembers(),0);assert.equal(f.entry.getSnapshot().phase,'welcome');assert.ok(f.disposed()>0);f.entry.dispose();
});
test('Hosted family retirement clears a private MFA challenge without admitting a profile',async()=>{
  const f=fixture();f.challenge();assert.equal(await f.entry.openHostedServer(hint,f.account),true);assert.equal(f.entry.getSnapshot().phase,'challenge');f.replace();
  assert.equal(f.entry.getSnapshot().phase,'welcome');assert.equal(await f.store.selected(),undefined);await f.entry.completeChallenge('123456');assert.equal(f.calls.includes('/v1/auth/two-factor'),false);f.entry.dispose();
});
