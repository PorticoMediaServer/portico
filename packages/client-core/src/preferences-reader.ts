import type {Routes} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {parsePreferenceSnapshot,validatePreferencePatch,withPreferencePatch,type DeviceClass,type PreferencePatch,type PreferenceScope,type PreferenceSnapshot} from './preferences.ts';

/**
 * A profile's settings on this device: `GET /v1/preferences` to read them, `PATCH /v1/preferences` to
 * save some at one scope. Screens subscribe and draw the latest snapshot.
 *
 * A save goes through the app's outbox like a watched mark: made while the server is out of reach,
 * it is kept on the device and sent when the server can be reached, and the screen shows it at once.
 * The server keeps the last save, so of two edits to one setting the later one wins.
 */
export type PreferencesApi=ServerRequester;
export type PreferenceObservation=
  | Readonly<{kind:'ok';value:PreferenceSnapshot}>
  | Readonly<{kind:'unreadable';error:unknown}>;

const kept=(error:unknown)=>(error as {code?:unknown}|null)?.code==='sync_pending';

export class PreferencesReader {
  private disposed=false;private requests=new Set<AbortController>();
  /** Requests are numbered as they start; an answer to a request older than the one shown is passed over. */
  private started=0;private shown=0;
  private snapshots=new Map<DeviceClass,PreferenceSnapshot>();private listeners=new Set<()=>void>();
  private readonly api:PreferencesApi;
  constructor(api:PreferencesApi){this.api=api;}
  getSnapshot=(deviceClass:DeviceClass='web')=>this.snapshots.get(deviceClass);
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  dispose(){this.disposed=true;for(const c of this.requests)c.abort();this.requests.clear();this.snapshots.clear();this.listeners.clear();}

  private adopt(snapshot:PreferenceSnapshot,request:number){
    if(this.disposed)throw new Error('This preferences scope is closed.');
    if(request<this.shown)return this.snapshots.get(snapshot.deviceClass)??snapshot;
    this.shown=request;this.snapshots.set(snapshot.deviceClass,snapshot);
    for(const listener of [...this.listeners])listener();
    return snapshot;
  }
  private async call<K extends keyof Routes>(route:K,options:Readonly<{query?:Record<string,string>;body?:Routes[K]['request']}>,signal?:AbortSignal):Promise<PreferenceSnapshot>{
    if(this.disposed)throw new Error('This preferences scope is closed.');
    signal?.throwIfAborted();
    const c=new AbortController();this.requests.add(c);
    const stop=()=>c.abort(signal?.reason);signal?.addEventListener('abort',stop,{once:true});
    try{
      const raw=await callServer(this.api,route,{...options,signal:c.signal});
      if(this.disposed||c.signal.aborted)throw new Error('Preferences request ended.');
      return parsePreferenceSnapshot(raw);
    }finally{this.requests.delete(c);signal?.removeEventListener('abort',stop);}
  }

  /** Reads the settings in force on a device of this kind. */
  async preferences(deviceClass:DeviceClass,signal?:AbortSignal):Promise<PreferenceSnapshot>{
    const request=++this.started;
    const snapshot=await this.call('GET /v1/preferences',{query:{deviceClass}},signal);
    if(snapshot.deviceClass!==deviceClass)throw new Error('Unreadable preferences response.');
    return this.adopt(snapshot,request);
  }
  /** The same read for a screen that wants an outcome instead of an exception. */
  async observePreferences(deviceClass:DeviceClass,signal?:AbortSignal):Promise<PreferenceObservation>{
    try{return {kind:'ok',value:await this.preferences(deviceClass,signal)};}
    catch(error){return {kind:'unreadable',error};}
  }
  /**
   * Saves `values` at `scope`: only the settings that change, and `null` to take a scope's value away.
   * Answers the settings as they now stand. When the server cannot be reached the edit is kept and
   * sent later, and the answer is the settings with the edit applied here.
   */
  async applyPreferences(scope:PreferenceScope,deviceClass:DeviceClass,values:PreferencePatch,signal?:AbortSignal):Promise<PreferenceSnapshot>{
    const current=this.snapshots.get(deviceClass);
    const patch=current?validatePreferencePatch(current.registry,scope,values):values;
    const request=++this.started;
    try{
      return this.adopt(await this.call('PATCH /v1/preferences',{body:{scope,deviceClass,values:patch as Routes['PATCH /v1/preferences']['request']['values']}},signal),request);
    }catch(error){
      if(!kept(error)||!current||this.disposed)throw error;
      return this.adopt(withPreferencePatch(this.snapshots.get(deviceClass)??current,scope,patch),request);
    }
  }
}
