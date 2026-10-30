import type {QuietRefreshOutcome} from '../freshness-actions.ts';
import {readLibraryChannels, readLibraryTemplates, type LibraryChannel, type LibraryChannelTemplate} from '../library-channels.ts';
import type {ChannelApi} from '../channel-guide.ts';
import {decodeGetAdminLibraryChannelsIdHealthResponse} from '../../../contracts/src/decoders.ts';
import {encodedRoutePath} from '../../../contracts/src/route-paths.ts';
import type {GetAdminLibraryChannelsIdHealthResponse} from '../../../contracts/src/types.ts';
export type ChannelHealth = GetAdminLibraryChannelsIdHealthResponse['result'];
export type LibraryChannelsSnapshot = Readonly<{phase:'idle'|'loading'|'ready'|'error'; channels?:readonly LibraryChannel[]; templates?:readonly LibraryChannelTemplate[]; templatesLoading:boolean; templatesError:boolean; selected?:LibraryChannel; health?:ChannelHealth; healthLoading:boolean; healthError:boolean}>;
const empty: LibraryChannelsSnapshot = Object.freeze({phase:'idle',templatesLoading:false,templatesError:false,healthLoading:false,healthError:false});
const terminal = (error:unknown) => {const e = (error && typeof error === 'object' ? error : {}) as {status?:number;code?:string};return e.status === 401 || e.status === 403 || ['unauthorized','forbidden','not_permitted'].includes(e.code ?? '');};

/** Bounded reads for the owner's library-channel list, template menu and exact
 * selected health panel. Playback and schedule generation stay server-owned. */
export class SettingsLibraryChannelsSource {
  private state = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private listRead?:AbortController;
  private templateRead?:AbortController;
  private healthRead?:AbortController;
  private quietRead?:AbortController;
  private quietSerial=0;
  constructor(private readonly api:ChannelApi,private readonly serverId:string,private readonly isCurrent:()=>boolean,private readonly reportError:(error:unknown)=>void) {}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = ():LibraryChannelsSnapshot => this.current() ? this.state : empty;
  subscribe = (listener:()=>void) => {this.listeners.add(listener);return ()=>{this.listeners.delete(listener);};};
  private publish(patch:Partial<LibraryChannelsSnapshot>) {if(this.current()){this.state=Object.freeze({...this.state,...patch});for(const listener of this.listeners)listener();}}
  private failure(error:unknown) {if(terminal(error)){this.invalidateQuiet();this.listRead?.abort();this.templateRead?.abort();this.healthRead?.abort();this.lifetime.abort();this.state=empty;for(const listener of this.listeners)listener();this.reportError(error);}}
  private transport(owns:()=>boolean):ChannelApi {return {request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>owns()?this.api.request<T>(path,method,body,signal):Promise.reject(Object.assign(new Error('The channel scope ended.'),{name:'AbortError'}))};}
  /** Mounted sibling write admission invalidates an older quiet read, even if the write settles first. */
  invalidateQuiet = () => {this.quietSerial++;this.quietRead?.abort();this.quietRead=undefined;};
  load = async ():Promise<void> => {
    this.invalidateQuiet();
    if(!this.current())return;
    this.listRead?.abort();this.healthRead?.abort();const read=new AbortController();this.listRead=read;
    const owns=()=>this.current()&&this.listRead===read&&!read.signal.aborted;
    this.publish({phase:'loading',selected:undefined,health:undefined,healthLoading:false,healthError:false});if(!owns())return;
    try{const channels=await readLibraryChannels(this.transport(owns),this.serverId,read.signal);if(new Set(channels.map(channel=>channel.config.id)).size!==channels.length)throw new Error('duplicate_channel_identity');if(owns())this.publish({phase:'ready',channels:Object.freeze([...channels].sort((a,b)=>a.config.position-b.config.position))});}
    catch(error){if(owns()){this.publish({phase:'error'});this.failure(error);}}
    finally{if(this.listRead===read)this.listRead=undefined;}
  };
  /** Refreshes the list and the exact open health target without resetting either surface. */
  refreshQuiet = async ():Promise<QuietRefreshOutcome> => {
    if(!this.current()||this.listRead||this.healthRead)return {kind:'deferred'};
    this.invalidateQuiet();
    const read=this.quietRead=new AbortController(),serial=this.quietSerial;
    const before=this.state.channels,selected=this.state.selected;
    const owns=()=>this.current()&&this.quietSerial===serial&&!read.signal.aborted&&this.state.channels===before&&this.state.selected===selected;
    try{
      const fresh=await readLibraryChannels(this.transport(owns),this.serverId,read.signal);
      if(!owns())return {kind:'deferred'};
      if(new Set(fresh.map(channel=>channel.config.id)).size!==fresh.length)throw new Error('duplicate_channel_identity');
      const old=new Map(before?.map(channel=>[channel.config.id,channel]));
      const channels=fresh.map(channel=>{
        const was=old.get(channel.config.id);
        return was&&JSON.stringify(was)===JSON.stringify(channel)?was:channel;
      }).sort((a,b)=>a.config.position-b.config.position);
      let health=this.state.health,nextSelected=selected;
      if(selected){
        try{
          const response=decodeGetAdminLibraryChannelsIdHealthResponse(await this.transport(owns).request(encodedRoutePath('get_admin_library_channels_id_health',{id:encodeURIComponent(selected.config.id)}),'GET',undefined,read.signal));
          if(!owns())return {kind:'deferred'};
          if(response.serverId!==this.serverId||response.result.channelId!==selected.config.id)throw new Error('invalid_channel_health_receipt');
          health=Object.freeze(response.result);
          nextSelected=channels.find(channel=>channel.config.id===selected.config.id);
          // The tolerant list may have dropped a row. Only exact-target proof may close it.
          if(!nextSelected){nextSelected=selected;channels.push(selected);channels.sort((a,b)=>a.config.position-b.config.position);}
        }catch(error){
          if(!owns())return {kind:'deferred'};
          if((error as {status?:number}|null)?.status!==404)throw error;
          nextSelected=undefined;health=undefined;
          const index=channels.findIndex(channel=>channel.config.id===selected.config.id);
          if(index>=0)channels.splice(index,1);
        }
      }
      if(!owns())return {kind:'deferred'};
      this.publish({phase:'ready',channels:Object.freeze(channels),selected:nextSelected,health,healthError:false});
      return {kind:'adopted'};
    }catch(error){
      if(!owns())return {kind:'deferred'};
      this.failure(error);
      return {kind:'unreadable',error};
    }finally{if(this.quietRead===read)this.quietRead=undefined;}
  };
  templates = async ():Promise<void> => {
    if(!this.current())return;
    this.templateRead?.abort();const read=new AbortController();this.templateRead=read;
    const owns=()=>this.current()&&this.templateRead===read&&!read.signal.aborted;
    this.publish({templatesLoading:true,templatesError:false});if(!owns())return;
    try{const templates=await readLibraryTemplates(this.transport(owns),this.serverId,read.signal);if(owns())this.publish({templates:Object.freeze(templates)});}
    catch(error){if(owns()){this.publish({templatesError:true});this.failure(error);}}
    finally{if(this.templateRead===read){this.templateRead=undefined;this.publish({templatesLoading:false});}}
  };
  health = async (channel:LibraryChannel):Promise<void> => {
    if(!this.current()||!this.state.channels?.includes(channel))return;
    this.invalidateQuiet();
    this.healthRead?.abort();const read=new AbortController();this.healthRead=read;
    const owns=()=>this.current()&&this.healthRead===read&&!read.signal.aborted&&this.state.selected===channel&&this.state.channels?.includes(channel)===true;
    this.publish({selected:channel,health:undefined,healthLoading:true,healthError:false});if(!owns())return;
    try{
      const response=decodeGetAdminLibraryChannelsIdHealthResponse(await this.transport(owns).request(encodedRoutePath('get_admin_library_channels_id_health',{id:encodeURIComponent(channel.config.id)}),'GET',undefined,read.signal));
      if(!owns())return;
      if(response.serverId!==this.serverId||response.result.channelId!==channel.config.id)throw new Error('invalid_channel_health_receipt');
      this.publish({health:Object.freeze(response.result)});
    }catch(error){if(owns()){this.publish({healthError:true});this.failure(error);}}
    finally{if(this.healthRead===read){this.healthRead=undefined;this.publish({healthLoading:false});}}
  };
  closeHealth = (expected?:LibraryChannel) => {if(expected && this.state.selected!==expected)return;this.invalidateQuiet();this.healthRead?.abort();this.healthRead=undefined;this.publish({selected:undefined,health:undefined,healthLoading:false,healthError:false});};
  dispose = () => {this.invalidateQuiet();this.listRead?.abort();this.templateRead?.abort();this.healthRead?.abort();this.lifetime.abort();this.state=empty;this.listeners.clear();};
}
