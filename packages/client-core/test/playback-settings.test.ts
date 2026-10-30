import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackSettingsService,parsePlaybackObservation} from '../src/playback-settings.ts';
const scope={serverId:'server-one',viewerId:'owner-one'};
const observation=()=>({scope:{serverId:'server-one',viewerFence:'a'.repeat(64)},observedAt:'2026-09-06T12:00:00Z',freshUntil:'2026-09-06T12:00:30Z',diagnostics:{directConfigured:true,hlsConfigured:true,finiteHlsConfigured:false,lifecycle:'running',activeConversionSessions:1,conversionSessionLimit:2,conversionLimitSource:'fixed_runtime',outputPolicy:'per_session_plan'}});
const deferred=()=>{let resolve!:(value:any)=>void;const promise=new Promise<any>(r=>{resolve=r});return{promise,resolve};};
test('playback settings reads bounded actual diagnostics and preserves stale snapshot on transient failure',async()=>{
 let fail=false;const service=new PlaybackSettingsService({scope,api:{requestBounded:async(path,max)=>{assert.equal(path,'/v1/admin/playback/diagnostics');assert.equal(max,8192);if(fail)throw {status:503,message:'SECRET'};return observation() as any;}}});
 await service.refresh();assert.equal(service.getSnapshot().phase,'ready');assert.equal(service.getSnapshot().observation?.diagnostics.activeConversionSessions,1);
 const prior=service.getSnapshot().observation;fail=true;await service.refresh();assert.equal(service.getSnapshot().phase,'error');assert.equal(service.getSnapshot().observation,prior);assert.equal(service.getSnapshot().canRefresh,true);assert.equal(JSON.stringify(service.getSnapshot()).includes('SECRET'),false);service.dispose();
});
test('playback settings owner revocation and scope changes erase observations and remove refresh capability',async()=>{
 for(const kind of ['401','403','server','fence']){
  let changed=false,calls=0;const service=new PlaybackSettingsService({scope,api:{requestBounded:async()=>{calls++;const raw=observation();if(changed){if(kind==='401'||kind==='403')throw {status:Number(kind)};if(kind==='server')raw.scope.serverId='another-server';else raw.scope.viewerFence='b'.repeat(64);}return raw as any;}}});
  await service.refresh();changed=true;await service.refresh();assert.equal(service.getSnapshot().phase,'access-denied');assert.equal(service.getSnapshot().observation,null);assert.equal(service.getSnapshot().canRefresh,false);await service.refresh();assert.equal(calls,2);service.dispose();
 }
});
test('playback settings timeout and disposal fence late transport completion',async()=>{
 const pending=deferred();let aborted=false;const service=new PlaybackSettingsService({scope,timeoutMs:5,api:{requestBounded:async(_p,_n,signal)=>{signal.addEventListener('abort',()=>{aborted=true});return pending.promise;}}});
 await service.refresh();assert.equal(aborted,true);assert.equal(service.getSnapshot().phase,'error');pending.resolve(observation());await Promise.resolve();assert.equal(service.getSnapshot().observation,null);service.dispose();
 const late=deferred();const old=new PlaybackSettingsService({scope,api:{requestBounded:()=>late.promise}});const read=old.refresh();old.dispose();late.resolve(observation());await read;assert.equal(old.getSnapshot().observation,null);
});
test('playback settings validates unavailable counts and rejects inconsistent capabilities',()=>{
 const raw=observation();raw.diagnostics={...raw.diagnostics,hlsConfigured:false,finiteHlsConfigured:false,lifecycle:'unavailable',activeConversionSessions:null as any,conversionSessionLimit:null as any,conversionLimitSource:'unavailable'};
 assert.equal(parsePlaybackObservation(raw).diagnostics.activeConversionSessions,null);
 for(const patch of [{finiteHlsConfigured:true},{activeConversionSessions:0},{outputPolicy:'anything'},{lifecycle:'running'}])assert.throws(()=>parsePlaybackObservation({...raw,diagnostics:{...raw.diagnostics,...patch}}));
 const active=observation();assert.throws(()=>parsePlaybackObservation({...active,diagnostics:{...active.diagnostics,activeConversionSessions:3}}));assert.throws(()=>parsePlaybackObservation({...active,freshUntil:'2026-09-06T12:02:00Z'}));
});
