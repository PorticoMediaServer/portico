import {routePath} from "../../contracts/src/route-paths.ts";
import type {ContentScope} from './library-content.ts';
import type {OperationsApi} from './server-operations.ts';
export type PlaybackDiagnostics={directConfigured:boolean;hlsConfigured:boolean;finiteHlsConfigured:boolean;lifecycle:'unavailable'|'running'|'stopping';activeConversionSessions:number|null;conversionSessionLimit:number|null;conversionLimitSource:'fixed_runtime'|'owner_policy'|'unavailable';outputPolicy:'per_session_plan'|'unavailable'};
export type PlaybackObservation={observedAt:string;freshUntil:string;diagnostics:Readonly<PlaybackDiagnostics>};
export type PlaybackSettingsSnapshot={phase:'idle'|'loading'|'ready'|'error'|'access-denied';observation:Readonly<PlaybackObservation>|null;error:string|null;canRefresh:boolean};
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const date=(v:unknown):v is string=>typeof v==='string'&&/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(v)&&Number.isFinite(Date.parse(v));
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
function invalid():never{throw new Error('Invalid playback observation.');}
export function parsePlaybackObservation(raw:unknown):Readonly<PlaybackObservation>{
 if(!object(raw)||!date(raw.observedAt)||!date(raw.freshUntil)||Date.parse(raw.freshUntil)-Date.parse(raw.observedAt)!==30000||!object(raw.diagnostics))invalid();
 const d=raw.diagnostics;
 if(typeof d.directConfigured!=='boolean'||typeof d.hlsConfigured!=='boolean'||typeof d.finiteHlsConfigured!=='boolean'||!['unavailable','running','stopping'].includes(String(d.lifecycle))||!['fixed_runtime','owner_policy','unavailable'].includes(String(d.conversionLimitSource))||!['per_session_plan','unavailable'].includes(String(d.outputPolicy)))invalid();
 if(d.finiteHlsConfigured&&!d.hlsConfigured)invalid();
 if(d.hlsConfigured){if(!count(d.activeConversionSessions)||d.outputPolicy!=='per_session_plan'||d.lifecycle==='unavailable')invalid();if(d.conversionLimitSource==='owner_policy'){if(d.conversionSessionLimit!==null)invalid();}else if(d.conversionLimitSource!=='fixed_runtime'||!count(d.conversionSessionLimit)||d.conversionSessionLimit<1||d.activeConversionSessions>d.conversionSessionLimit)invalid();}
 else if(d.activeConversionSessions!==null||d.conversionSessionLimit!==null||d.conversionLimitSource!=='unavailable'||d.outputPolicy!==(d.directConfigured?'per_session_plan':'unavailable')||d.lifecycle!=='unavailable')invalid();
 return Object.freeze({observedAt:raw.observedAt,freshUntil:raw.freshUntil,diagnostics:Object.freeze({directConfigured:d.directConfigured,hlsConfigured:d.hlsConfigured,finiteHlsConfigured:d.finiteHlsConfigured,lifecycle:d.lifecycle as PlaybackDiagnostics['lifecycle'],activeConversionSessions:d.activeConversionSessions as number|null,conversionSessionLimit:d.conversionSessionLimit as number|null,conversionLimitSource:d.conversionLimitSource as PlaybackDiagnostics['conversionLimitSource'],outputPolicy:d.outputPolicy as PlaybackDiagnostics['outputPolicy']})});
}
export class PlaybackSettingsService{
 private state:PlaybackSettingsSnapshot={phase:'idle',observation:null,error:null,canRefresh:true};
 private listeners=new Set<()=>void>();private disposed=false;private fenced=false;private fence?:string;private pending?:AbortController;
 private options:{api:OperationsApi;scope:ContentScope;timeoutMs?:number};
 constructor(options:{api:OperationsApi;scope:ContentScope;timeoutMs?:number}){if(!options.scope.serverId||!options.scope.viewerId)throw new Error('A selected server owner is required.');if(options.timeoutMs!==undefined&&(!Number.isFinite(options.timeoutMs)||options.timeoutMs<1||options.timeoutMs>30000))throw new Error('Invalid observation timeout.');this.options={...options,scope:{...options.scope}};}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(state:PlaybackSettingsSnapshot){if(this.disposed)return;this.state=Object.freeze(state);for(const fn of this.listeners)fn();}
 private revoke(){this.fenced=true;this.pending?.abort();this.publish({phase:'access-denied',observation:null,error:'Owner access changed. Reopen this server after signing in.',canRefresh:false});}
 dispose(){this.disposed=true;this.pending?.abort();this.pending=undefined;this.listeners.clear();}
 async refresh():Promise<void>{
  if(this.disposed||this.fenced||this.pending)return;
  const c=new AbortController();this.pending=c;const previous=this.state.observation;this.publish({phase:'loading',observation:previous,error:null,canRefresh:false});let timer:ReturnType<typeof setTimeout>|undefined;
  try{
   const raw=await Promise.race([this.options.api.requestBounded<unknown>(routePath("get_admin_playback_diagnostics"),8192,c.signal),new Promise<never>((_,reject)=>{timer=setTimeout(()=>{c.abort();reject(new Error('Playback observation timed out.'));},this.options.timeoutMs??7000);})]);
   if(this.disposed||this.fenced||c.signal.aborted)return;
   if(!object(raw)||!object(raw.scope)||raw.scope.serverId!==this.options.scope.serverId||typeof raw.scope.viewerFence!=='string'||!/^[a-f0-9]{64}$/.test(raw.scope.viewerFence)||this.fence&&this.fence!==raw.scope.viewerFence){this.revoke();return;}
   const observation=parsePlaybackObservation(raw);this.fence=raw.scope.viewerFence;this.publish({phase:'ready',observation,error:null,canRefresh:true});
  }catch(error){if(this.disposed||this.fenced)return;if(object(error)&&(error.status===401||error.status===403)){this.revoke();return;}this.publish({phase:'error',observation:previous,error:'Playback observations could not be refreshed. Try again.',canRefresh:true});}
  finally{if(timer)clearTimeout(timer);if(this.pending===c)this.pending=undefined;}
 }
}
