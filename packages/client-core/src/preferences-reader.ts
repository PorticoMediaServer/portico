import {routePath} from "../../contracts/src/route-paths.ts";
import {parsePreferenceSnapshot,currentPreferenceSnapshot,type DeviceClass,type PreferencePatch,type PreferenceScope,type PreferenceSnapshot} from './preferences.ts';

/**
 * Minimal viewer-preferences reader (PERF-25): `GET`/`PATCH /v1/preferences`
 * without the owner-console client (`console.ts`) and its telemetry,
 * attention and playback-history parsers, which every viewer would otherwise
 * download at startup to read a few settings.
 *
 * Contract mirror of `ConsoleClient.preferences`/`applyPreferences`: the
 * server wraps data in `{scope: {serverId, viewerFence}, data}`, another
 * server's answer ends this scope, a read under a moved viewer fence is the
 * newer answer (the viewer's access was decided again; it never ends the
 * shell), a write answer under a moved fence is refused, and a PATCH
 * carries an idempotency key, so a retry of a save whose answer was lost is
 * the same save.
 */
export type PreferencesApi={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>;requestConsole?<T>(path:string,method:string,body:unknown,signal:AbortSignal):Promise<T>};
export type PreferencesScope={serverId:string;viewerId:string};
export type PreferenceObservation=
  | Readonly<{kind:'ok';value:PreferenceSnapshot}>
  | Readonly<{kind:'scope-changed'}>
  | Readonly<{kind:'unreadable';error:unknown}>;

class PreferenceScopeChanged extends Error {}
/** A read that started before the read that taught the current fence answered under another fence: it is older. */
class PreferenceReadSuperseded extends Error {}

const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);

/** Operation keys are uniqueness tokens, not credentials or authorization. */
export function preferencesOperationId():string{return globalThis.crypto?.randomUUID?.()??`op-${Date.now().toString(36)}-${Math.random().toString(36).slice(2,16)}`;}

export class PreferencesReader {
  private disposed=false;private fence?:string;private requests=new Set<AbortController>();/** Reads started, and the start of the latest read whose answer the current fence came from. */private reads=0;private fenceRead=0;
  private snapshots=new Map<DeviceClass,PreferenceSnapshot>();private listeners=new Set<()=>void>();
  private pending?:{body:string;key:string};
  getSnapshot=(deviceClass:DeviceClass='web')=>this.snapshots.get(deviceClass);
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private adopt(incoming:PreferenceSnapshot){if(this.disposed)throw new Error('This preferences scope is closed.');const current=this.snapshots.get(incoming.deviceClass);const next=currentPreferenceSnapshot(current,incoming);if(current!==next){this.snapshots.set(next.deviceClass,next);for(const listener of this.listeners)listener();}return next;}
  private api:PreferencesApi;readonly scope:PreferencesScope;
  constructor(api:PreferencesApi,scope:PreferencesScope){if(!scope.serverId||!scope.viewerId)throw new Error('A selected viewer is required.');this.api=api;this.scope={...scope};}
  dispose(){this.disposed=true;for(const c of this.requests)c.abort();this.requests.clear();this.snapshots.clear();this.listeners.clear();}
  /** The fence an answer names. Another server's answer is refused by the caller. A write's answer under a moved fence
   * is refused; a read's is the newer answer (step 7: the viewer's access was decided again), taking the place of every
   * snapshot read under the old fence, unless that read started before the read that taught the current fence. */
  private admitFence(viewerFence:string,read:number|undefined){
    if(this.fence!==undefined&&this.fence!==viewerFence){
      if(read===undefined)throw new Error('The selected viewer changed. Reopen this server.');
      if(read<this.fenceRead)throw new PreferenceReadSuperseded('An older preferences read.');
      this.snapshots.clear();
    }
    if(read!==undefined)this.fenceRead=Math.max(this.fenceRead,read);
    this.fence=viewerFence;
  }
  private async call<T>(path:string,method:string,body:unknown,parse:(v:unknown)=>T,observation?:{signal?:AbortSignal}):Promise<T>{
    if(this.disposed)throw new Error('This preferences scope is closed.');
    const read=method==='GET'?++this.reads:undefined;
    if(observation?.signal?.aborted)throw observation.signal.reason??new Error('Preferences request ended.');
    const c=new AbortController();this.requests.add(c);const timer=setTimeout(()=>c.abort(),10000);
    const stop=()=>c.abort(observation?.signal?.reason);
    observation?.signal?.addEventListener('abort',stop,{once:true});
    try{
      const raw=await (this.api.requestConsole?this.api.requestConsole<unknown>(path,method,body,c.signal):this.api.request<unknown>(path,method,body,c.signal));
      if(this.disposed||c.signal.aborted)throw new Error('Preferences request ended.');
      if(observation){
        // Parse the complete data even for a foreign scope: malformed proof cannot
        // establish that the selected viewer changed.
        if(!object(raw)||!object(raw.scope)||typeof raw.scope.serverId!=='string'||!raw.scope.serverId||typeof raw.scope.viewerFence!=='string'||!/^[a-f0-9]{64}$/.test(raw.scope.viewerFence))throw new Error('Unreadable preferences response.');
        const out=parse(raw.data);
        if(raw.scope.serverId!==this.scope.serverId)throw new PreferenceScopeChanged('The selected viewer changed. Reopen this server.');
        this.admitFence(raw.scope.viewerFence,read);return out;
      }
      if(!object(raw)||!object(raw.scope)||raw.scope.serverId!==this.scope.serverId||typeof raw.scope.viewerFence!=='string'||!/^[a-f0-9]{64}$/.test(raw.scope.viewerFence))throw new Error('The selected viewer changed. Reopen this server.');
      const out=parse(raw.data);this.admitFence(raw.scope.viewerFence,read);return out;
    }finally{clearTimeout(timer);this.requests.delete(c);observation?.signal?.removeEventListener('abort',stop);}
  }
  preferences(deviceClass:DeviceClass){return this.call(routePath("get_preferences") + "?deviceClass=" + (deviceClass),'GET',undefined,parsePreferenceSnapshot).then(snapshot=>this.adopt(snapshot),error=>{
    // An older read's answer: the snapshot a newer read gave stands.
    const held=error instanceof PreferenceReadSuperseded?this.snapshots.get(deviceClass):undefined;if(held)return held;throw error;});}
  /** Cancellable observation on this reader's existing parser/store; defaults stay independent. */
  async observePreferences(deviceClass:DeviceClass,signal?:AbortSignal):Promise<PreferenceObservation>{
    try{
      const snapshot=await this.call(routePath("get_preferences")+"?deviceClass="+deviceClass,'GET',undefined,raw=>{const parsed=parsePreferenceSnapshot(raw);if(parsed.deviceClass!==deviceClass)throw new Error('Unreadable preferences response.');return parsed;},{signal});
      if(this.disposed||signal?.aborted)throw signal?.reason??new Error('Preferences request ended.');
      return {kind:'ok',value:this.adopt(snapshot)};
    }catch(error){
      const held=error instanceof PreferenceReadSuperseded?this.snapshots.get(deviceClass):undefined;if(held)return {kind:'ok',value:held};
      return error instanceof PreferenceScopeChanged?{kind:'scope-changed'}:{kind:'unreadable',error};
    }
  }
  /** Consume the original result separately from a new edit after restart. */
  async reconcilePending(){
    const pending=this.pending;if(!pending)return;
    const snapshot=await this.call(routePath('patch_preferences'),'PATCH',{...JSON.parse(pending.body),idempotencyKey:pending.key},parsePreferenceSnapshot);
    if(this.pending===pending)this.pending=undefined;return this.adopt(snapshot);
  }
  /** `values` carries only the keys that change; `null` clears that override. */
  async applyPreferences(scope:PreferenceScope,deviceClass:DeviceClass,expectedRevision:number,values:PreferencePatch){
    const body=JSON.stringify({scope,deviceClass,expectedRevision,values});
    if(this.pending&&this.pending.body!==body)throw new Error('A previous action has an unconfirmed result. Review the original draft before submitting different values.');
    const pending=this.pending??={body,key:preferencesOperationId()};
    try{
      const snapshot=await this.call(routePath('patch_preferences'),'PATCH',{...JSON.parse(pending.body),idempotencyKey:pending.key},parsePreferenceSnapshot);
      if(this.pending===pending)this.pending=undefined;
      return this.adopt(snapshot);
    }catch(error){
      // A definite refusal ends this save. A save with no answer keeps its key, so trying again is the same save.
      if(object(error)&&[400,404,409,410,412,428,429].includes(Number(error.status))&&this.pending===pending)this.pending=undefined;
      throw error;
    }
  }
}
