import {clonePortableDto} from '../clone-portable-dto.ts';
import {parseLibraryChannel,saveLibraryChannel,type LibraryChannel} from '../library-channels.ts';
import {channelRequestID} from '../live-source-draft.ts';
import {regenerationBody} from '../server-admin/panel-logic.ts';
import type {ChannelApi} from '../channel-guide.ts';
import {decodePostAdminLibraryChannelsIdDeleteResponse,decodePostAdminLibraryChannelsIdRegenerateResponse,decodePostAdminLibraryChannelsReorderResponse} from '../../../contracts/src/decoders.ts';
import {encodedRoutePath,routePath} from '../../../contracts/src/route-paths.ts';
import type {SettingsLibraryChannelsSource} from './channels-source.ts';
type Kind='toggle'|'regenerate'|'move'|'delete';
type Pending=Readonly<{kind:Kind;channelId:string;name:string}>;
type Intent=Readonly<{pending:Pending;send:(api:ChannelApi,signal:AbortSignal)=>Promise<void>}>;
export type ChannelActionsSnapshot=Readonly<{busy:boolean;pending?:Pending;error?:'changed'|'write';done?:Kind}>;
const empty:ChannelActionsSnapshot=Object.freeze({busy:false});
const terminal=(error:unknown)=>{const e=(error&&typeof error==='object'?error:{}) as {status?:number;code?:string};return e.status===401||e.status===403||['unauthorized','forbidden','not_permitted'].includes(e.code??'');};
function frozen<T>(value:T):T{if(value&&typeof value==='object'){for(const child of Object.values(value))frozen(child);Object.freeze(value);}return value;}

/** Metadata actions preserve the server's advertised revision and regeneration
 * boundary. Uncertain responses retain exactly one request, never a new ID. */
export class SettingsChannelActionsSource{
  private state=empty;
  private readonly listeners=new Set<()=>void>();
  private readonly lifetime=new AbortController();
  private flight?:AbortController;
  private intent?:Intent;
  constructor(private readonly api:ChannelApi,private readonly serverId:string,private readonly reader:SettingsLibraryChannelsSource,private readonly isCurrent:()=>boolean,private readonly reportError:(error:unknown)=>void,private readonly operationId:()=>string=channelRequestID){}
  private current(){return !this.lifetime.signal.aborted&&this.isCurrent();}
  getSnapshot=():ChannelActionsSnapshot=>this.current()?this.state:empty;
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private publish(patch:Partial<ChannelActionsSnapshot>){if(this.current()){this.state=Object.freeze({...this.state,...patch});for(const listener of this.listeners)listener();}}
  private admitted(channel:LibraryChannel){return this.current()&&!this.flight&&!this.intent&&this.reader.getSnapshot().phase==='ready'&&this.reader.getSnapshot().channels?.includes(channel);}
  private envelope(value:{serverId:string;protocolVersion:string}){if(value.serverId!==this.serverId||value.protocolVersion!=='1.0')throw new Error('invalid_channel_action_receipt');}
  perform=async(channel:LibraryChannel,kind:Kind,direction?:-1|1,targetIndex?:number):Promise<boolean>=>{
    if(!this.admitted(channel))return false;
    const list=this.reader.getSnapshot().channels!;let send:Intent['send'];
    try{
      const requestId=this.operationId();
      if(kind==='toggle'){
        const config=frozen({...clonePortableDto(channel.config),enabled:!channel.config.enabled}),revision=channel.revision;
        send=async(api,signal)=>{const result=await saveLibraryChannel(api,this.serverId,config,revision,requestId,signal);if(result.config.id!==config.id||result.config.enabled!==config.enabled||result.revision<=revision)throw new Error('invalid_channel_toggle_receipt');};
      }else if(kind==='regenerate'){
        if(!channel.replacementBoundary)return false;const body=frozen(regenerationBody(channel,requestId)),id=channel.config.id;
        send=async(api,signal)=>{const response=decodePostAdminLibraryChannelsIdRegenerateResponse(await api.request(encodedRoutePath('post_admin_library_channels_id_regenerate',{id:encodeURIComponent(id)}),'POST',body,signal));this.envelope(response);if(parseLibraryChannel(response.channel).config.id!==id)throw new Error('invalid_channel_regenerate_receipt');};
      }else if(kind==='delete'){
        const id=channel.config.id,body=Object.freeze({expectedRevision:channel.revision,requestId});
        send=async(api,signal)=>{const response=decodePostAdminLibraryChannelsIdDeleteResponse(await api.request(encodedRoutePath('post_admin_library_channels_id_delete',{id:encodeURIComponent(id)}),'POST',body,signal));this.envelope(response);if(!response.deleted)throw new Error('invalid_channel_delete_receipt');};
      }else{
        // A drag of any distance is one reorder of the whole list, never a run of adjacent swaps.
        const index=list.indexOf(channel),target=targetIndex??(direction===-1||direction===1?index+direction:-1);
        if(!Number.isSafeInteger(target)||target<0||target>=list.length||target===index)return false;
        const ordered=[...list];ordered.splice(target,0,...ordered.splice(index,1));
        const body=frozen({requestId,channels:ordered.map(row=>({id:row.config.id,expectedRevision:row.revision}))});
        send=async(api,signal)=>{const response=decodePostAdminLibraryChannelsReorderResponse(await api.request(routePath('post_admin_library_channels_reorder'),'POST',body,signal));this.envelope(response);if(!response.saved)throw new Error('invalid_channel_order_receipt');};
      }
    }catch{this.publish({error:'write'});return false;}
    if(!this.admitted(channel)||this.reader.getSnapshot().channels!==list)return false;
    const intent=Object.freeze({pending:Object.freeze({kind,channelId:channel.config.id,name:channel.config.name}),send});this.intent=intent;return this.run(intent);
  };
  /** Moves the channel to `targetIndex` in the current list with one reorder request. */
  moveTo=(channel:LibraryChannel,targetIndex:number):Promise<boolean>=>this.perform(channel,'move',undefined,targetIndex);
  retry=async():Promise<boolean>=>{const intent=this.intent;return intent?this.run(intent):false;};
  private async run(intent:Intent):Promise<boolean>{
    if(!this.current()||this.flight||this.intent!==intent)return false;
    const flight=new AbortController();this.flight=flight;const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    this.publish({busy:true,pending:intent.pending,error:undefined,done:undefined});let completed=false;
    try{
      if(!owns())return false;
      const api:ChannelApi={request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>owns()?this.api.request<T>(path,method,body,signal):Promise.reject(Object.assign(new Error('The channel action ended.'),{name:'AbortError'}))};
      await intent.send(api,flight.signal);if(!owns())return false;this.intent=undefined;completed=true;this.publish({pending:undefined,done:intent.pending.kind});
    }catch(error){if(owns()){
      if(terminal(error)){this.dispose();this.reportError(error);}
      else{const status=(error&&typeof error==='object'?error:{}) as {status?:number};if([400,412,422].includes(status.status??0)){this.intent=undefined;this.publish({pending:undefined});}this.publish({error:status.status===412?'changed':'write'});}
    }}finally{if(this.flight===flight){this.flight=undefined;this.publish({busy:false});}}
    if(completed&&this.current())await this.reader.load();return completed&&this.current();
  }
  dispose=()=>{this.flight?.abort();this.flight=undefined;this.intent=undefined;this.lifetime.abort();this.state=empty;for(const listener of this.listeners)listener();this.listeners.clear();};
}
