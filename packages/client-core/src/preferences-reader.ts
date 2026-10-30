import {routePath} from "../../contracts/src/route-paths.ts";
import {sameUserMutationScope,type UserMutationCoordinator,type UserMutationScope} from './user-mutations.ts';
import {parsePreferenceSnapshot,currentPreferenceSnapshot,type DeviceClass,type PreferencePatch,type PreferenceScope,type PreferenceSnapshot} from './preferences.ts';

/**
 * Minimal viewer-preferences reader (PERF-25): `GET`/`PATCH /v1/preferences`
 * without the owner-console client (`console.ts`) and its telemetry,
 * attention and playback-history parsers, which every viewer would otherwise
 * download at startup to read a few settings.
 *
 * Contract mirror of `ConsoleClient.preferences`/`applyPreferences`: the
 * server wraps data in `{scope: {serverId, viewerFence}, data}`, the fence
 * pins the selected viewer (a changed fence ends this scope), and a PATCH
 * carries an idempotency key. The mounted transport owns durable custody;
 * manual retries reconnect to that intent rather than authoring another one.
 */
export type PreferencesApi={awaitUserMutationsReady?():Promise<void>;getUserMutationScope?():UserMutationScope|undefined;getUserMutationCoordinator?():UserMutationCoordinator|undefined;acknowledgeUserMutation?(intentId:string):Promise<void>;request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>;requestConsole?<T>(path:string,method:string,body:unknown,signal:AbortSignal):Promise<T>};
export type PreferencesScope={serverId:string;viewerId:string};
export type PreferenceObservation=
  | Readonly<{kind:'ok';value:PreferenceSnapshot}>
  | Readonly<{kind:'scope-changed'}>
  | Readonly<{kind:'unreadable';error:unknown}>;

class PreferenceScopeChanged extends Error {}

const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);

/** Operation keys are uniqueness tokens, not credentials or authorization. */
export function preferencesOperationId():string{return globalThis.crypto?.randomUUID?.()??`op-${Date.now().toString(36)}-${Math.random().toString(36).slice(2,16)}`;}

export class PreferencesReader {
  private disposed=false;private fence?:string;private requests=new Set<AbortController>();
  private snapshots=new Map<DeviceClass,PreferenceSnapshot>();private listeners=new Set<()=>void>();
  private pending?:{body:string;key:string;binding?:UserMutationScope;ack?:Promise<void>};
  getSnapshot=(deviceClass:DeviceClass='web')=>this.snapshots.get(deviceClass);
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private adopt(incoming:PreferenceSnapshot){if(this.disposed)throw new Error('This preferences scope is closed.');const current=this.snapshots.get(incoming.deviceClass);const next=currentPreferenceSnapshot(current,incoming);if(current!==next){this.snapshots.set(next.deviceClass,next);for(const listener of this.listeners)listener();}return next;}
  private api:PreferencesApi;readonly scope:PreferencesScope;
  constructor(api:PreferencesApi,scope:PreferencesScope){if(!scope.serverId||!scope.viewerId)throw new Error('A selected viewer is required.');this.api=api;this.scope={...scope};}
  dispose(){this.disposed=true;for(const c of this.requests)c.abort();this.requests.clear();this.snapshots.clear();this.listeners.clear();}
  private async call<T>(path:string,method:string,body:unknown,parse:(v:unknown)=>T,custody?:{ack?:Promise<void>},observation?:{signal?:AbortSignal}):Promise<T>{
    if(this.disposed)throw new Error('This preferences scope is closed.');
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
        if(raw.scope.serverId!==this.scope.serverId||this.fence!==undefined&&this.fence!==raw.scope.viewerFence)throw new PreferenceScopeChanged('The selected viewer changed. Reopen this server.');
        this.fence=raw.scope.viewerFence;return out;
      }
      if(!object(raw)||!object(raw.scope)||raw.scope.serverId!==this.scope.serverId||typeof raw.scope.viewerFence!=='string'||!/^[a-f0-9]{64}$/.test(raw.scope.viewerFence)||this.fence!==undefined&&this.fence!==raw.scope.viewerFence)throw new Error('The selected viewer changed. Reopen this server.');
      const out=parse(raw.data);this.fence=raw.scope.viewerFence;const intent=body&&typeof body==='object'?(body as Record<string,unknown>).idempotencyKey:undefined;if(typeof intent==='string'){if(custody)await(custody.ack??=(this.api.acknowledgeUserMutation?.(intent)??Promise.resolve()).catch(error=>{custody.ack=undefined;throw error;}));else await this.api.acknowledgeUserMutation?.(intent);}return out;
    }finally{clearTimeout(timer);this.requests.delete(c);observation?.signal?.removeEventListener('abort',stop);}
  }
  preferences(deviceClass:DeviceClass){return this.call(routePath("get_preferences") + "?deviceClass=" + (deviceClass),'GET',undefined,parsePreferenceSnapshot).then(snapshot=>this.adopt(snapshot));}
  /** Cancellable observation on this reader's existing parser/store; defaults stay independent. */
  async observePreferences(deviceClass:DeviceClass,signal?:AbortSignal):Promise<PreferenceObservation>{
    try{
      const snapshot=await this.call(routePath("get_preferences")+"?deviceClass="+deviceClass,'GET',undefined,raw=>{const parsed=parsePreferenceSnapshot(raw);if(parsed.deviceClass!==deviceClass)throw new Error('Unreadable preferences response.');return parsed;},undefined,{signal});
      if(this.disposed||signal?.aborted)throw signal?.reason??new Error('Preferences request ended.');
      return {kind:'ok',value:this.adopt(snapshot)};
    }catch(error){return error instanceof PreferenceScopeChanged?{kind:'scope-changed'}:{kind:'unreadable',error};}
  }
  /** Consume the original result separately from a new edit after restart. */
  async reconcilePending(){
    const coordinator=this.api.getUserMutationCoordinator?.();
    if(coordinator){
      await this.api.awaitUserMutationsReady?.();await coordinator.reconcile();const live=this.api.getUserMutationScope?.();
      const retained=coordinator.snapshot().find(entry=>live&&sameUserMutationScope(entry.scope,live)&&entry.path===routePath('patch_preferences')&&entry.method==='PATCH');
      if(!this.pending&&retained){
        const body=object(retained.body)?{...retained.body}:{deviceClass:retained.recoveryDeviceClass};delete (body as Record<string,unknown>).idempotencyKey;
        this.pending={body:JSON.stringify(body),key:retained.intentId,binding:retained.scope};
      }
    }
    const pending=this.pending;if(!pending)return;
    const live=this.api.getUserMutationScope?.();if(pending.binding&&(!live||!sameUserMutationScope(pending.binding,live)))throw new Error('The selected viewer changed. Review the original pending action.');
    const snapshot=await this.call(routePath('patch_preferences'),'PATCH',{...JSON.parse(pending.body),idempotencyKey:pending.key},parsePreferenceSnapshot,pending);
    if(this.pending===pending)this.pending=undefined;return this.adopt(snapshot);
  }
  /** `values` carries only the keys that change; `null` clears that override. */
  async applyPreferences(scope:PreferenceScope,deviceClass:DeviceClass,expectedRevision:number,values:PreferencePatch){
    const body=JSON.stringify({scope,deviceClass,expectedRevision,values}),coordinator=this.api.getUserMutationCoordinator?.();
    if(coordinator){
      await this.api.awaitUserMutationsReady?.();await coordinator.reconcile();
      const live=this.api.getUserMutationScope?.();
      const retained=coordinator.snapshot().find(entry=>live&&sameUserMutationScope(entry.scope,live)&&entry.path===routePath('patch_preferences')&&entry.method==='PATCH');
      if(!this.pending&&retained){
        const frozen=object(retained.body)?{...retained.body}:undefined;if(frozen)delete frozen.idempotencyKey;
        if(!frozen)throw new Error('A previous action has an unconfirmed result. Reconcile it before submitting another draft.');
        this.pending={body:JSON.stringify(frozen),key:retained.intentId,binding:retained.scope};
      }
    }
    if(this.pending&&this.pending.body!==body)throw new Error('A previous action has an unconfirmed result. Review the original draft before submitting different values.');
    const pending=this.pending??={body,key:preferencesOperationId(),binding:this.api.getUserMutationScope?.()};
    const live=this.api.getUserMutationScope?.();if(pending.binding&&(!live||!sameUserMutationScope(pending.binding,live)))throw new Error('The selected viewer changed. Review the original pending action.');
    try{
      const snapshot=await this.call(routePath('patch_preferences'),'PATCH',{...JSON.parse(pending.body),idempotencyKey:pending.key},parsePreferenceSnapshot,pending);
      if(this.pending===pending)this.pending=undefined;
      return this.adopt(snapshot);
    }catch(error){
      // Legacy transports have no durable owner. Definite refusals keep their
      // existing edit flow; ambiguous answers still retain the original key.
      if(!coordinator&&object(error)&&[400,404,409,410,412,428,429].includes(Number(error.status))&&this.pending===pending)this.pending=undefined;
      throw error;
    }
  }
}
