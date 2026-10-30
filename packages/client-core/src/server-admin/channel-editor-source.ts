import {decodeGetAdminLibraryChannelsIdHealthResponse} from '../../../contracts/src/decoders.ts';
import {encodedRoutePath} from '../../../contracts/src/route-paths.ts';
import {rebaseDraft, type QuietRefreshOutcome} from '../freshness-actions.ts';
import {clonePortableDto} from '../clone-portable-dto.ts';
import {previewLibraryChannel,readLibraryDefaults,saveLibraryChannel,type LibraryChannel,type LibraryChannelConfig,type LibraryChannelPreview,type LibraryChannelTemplate} from '../library-channels.ts';
import {channelRequestID} from '../live-source-draft.ts';
import type {ChannelApi} from '../channel-guide.ts';
import type {SettingsLibraryChannelsSource} from './channels-source.ts';
type Intent=Readonly<{config:LibraryChannelConfig;revision:number;requestId:string}>;
export type ChannelEditorSnapshot=Readonly<{phase:'closed'|'loading'|'editing';config?:LibraryChannelConfig;templateName?:string;busy:boolean;pending:boolean;error?:'read'|'changed'|'save'|'preview';preview?:LibraryChannelPreview;previewLoading:boolean}>;
const empty:ChannelEditorSnapshot=Object.freeze({phase:'closed',busy:false,pending:false,previewLoading:false});
const terminal=(error:unknown)=>{const e=(error&&typeof error==='object'?error:{}) as {status?:number;code?:string};return e.status===401||e.status===403||['unauthorized','forbidden','not_permitted'].includes(e.code??'');};
function frozen<T>(value:T):T {if(value&&typeof value==='object'){for(const child of Object.values(value))frozen(child);Object.freeze(value);}return value;}

/** One explicit editor opening owns its reads, preview and immutable save
 * request. An uncertain save can only retry its original body/revision/ID. */
export class SettingsChannelEditorSource {
  private state=empty;
  private readonly listeners=new Set<()=>void>();
  private readonly lifetime=new AbortController();
  private opening?:AbortController;
  private previewRead?:AbortController;
  private flight?:AbortController;
  private intent?:Intent;
  private original?:LibraryChannel;
  private revision=0;
  private quietConflict=false;
  private quietRead?:AbortController;
  invalidateQuiet=()=>{this.quietRead?.abort();this.quietRead=undefined;};
  private inputActive=false;
  private readonly incomplete=new Set<string>();
  quietBlocked=()=>this.inputActive||this.incomplete.size>0||!!this.flight||!!this.intent;
  setQuietInputActive=(active:boolean)=>{
    if(!this.current()||this.inputActive===active)return;
    this.inputActive=active;if(active){this.invalidateQuiet();this.reader.invalidateQuiet();}this.publish({});
  };
  setQuietIncomplete=(part:'conditions'|'hours',active:boolean)=>{
    if(!this.current()||this.incomplete.has(part)===active)return;
    if(active){this.incomplete.add(part);this.reader.invalidateQuiet();}else this.incomplete.delete(part);
    this.publish({});
  };
  constructor(private readonly api:ChannelApi,private readonly serverId:string,private readonly reader:SettingsLibraryChannelsSource,private readonly isCurrent:()=>boolean,private readonly reportError:(error:unknown)=>void,private readonly operationId:()=>string=channelRequestID){}
  private current(){return !this.lifetime.signal.aborted&&this.isCurrent();}
  getSnapshot=():ChannelEditorSnapshot=>this.current()?this.state:empty;
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private publish(patch:Partial<ChannelEditorSnapshot>){if(this.current()){this.state=Object.freeze({...this.state,...patch});for(const listener of this.listeners)listener();}}
  private failure(error:unknown,kind:NonNullable<ChannelEditorSnapshot['error']>){if(terminal(error)){this.retire();this.reportError(error);}else this.publish({error:kind});}
  private transport(owns:()=>boolean):ChannelApi{return{request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>owns()?this.api.request<T>(path,method,body,signal):Promise.reject(Object.assign(new Error('The editor scope ended.'),{name:'AbortError'}))};}
  private begin(){if(!this.current()||this.flight||this.intent)return;this.invalidateQuiet();this.opening?.abort();this.previewRead?.abort();const opening=new AbortController();this.opening=opening;this.original=undefined;this.quietConflict=false;this.inputActive=false;this.incomplete.clear();this.revision=0;return opening;}
  blank=async(timezone:string):Promise<void>=>{
    const opening=this.begin();if(!opening)return;const owns=()=>this.current()&&this.opening===opening&&!opening.signal.aborted;
    this.publish({...empty,phase:'loading'});if(!owns())return;
    try{const config=await readLibraryDefaults(this.transport(owns),this.serverId,timezone,opening.signal);if(owns())this.publish({phase:'editing',config:frozen(clonePortableDto(config))});}
    catch(error){if(owns())this.failure(error,'read');}
  };
  template=(template:LibraryChannelTemplate,timezone:string):void=>{
    if(!this.current()||!template.applicable||!this.reader.getSnapshot().templates?.includes(template))return;
    let config:LibraryChannelConfig;
    try{config=frozen({...clonePortableDto(template.config),id:'lc-'+this.operationId().slice(0,24),timezone,templateId:template.id});}catch(error){this.failure(error,'read');return;}
    const opening=this.begin();if(!opening)return;this.publish({...empty,phase:'editing',config,templateName:template.name});
  };
  edit=(channel:LibraryChannel):void=>{
    if(!this.current()||!this.reader.getSnapshot().channels?.includes(channel))return;
    const opening=this.begin();if(!opening)return;this.original=channel;this.revision=channel.revision;
    this.publish({...empty,phase:'editing',config:frozen(clonePortableDto(channel.config))});
  };
  change=(update:(config:LibraryChannelConfig)=>LibraryChannelConfig):void=>{
    const before=this.state.config;if(!this.current()||this.flight||this.intent||!before||this.state.phase!=='editing')return;
    const next=update(clonePortableDto(before));if(!this.current()||this.state.config!==before||next.id!==before.id)return;
    this.previewRead?.abort();this.previewRead=undefined;this.publish({config:frozen(clonePortableDto(next)),preview:undefined,previewLoading:false,error:undefined});
  };
  preview=async():Promise<void>=>{
    const config=this.state.config;if(!this.current()||this.flight||!config||this.state.phase!=='editing')return;
    this.previewRead?.abort();const read=new AbortController();this.previewRead=read;
    const owns=()=>this.current()&&this.previewRead===read&&!read.signal.aborted&&this.state.config===config&&!this.flight;
    this.publish({previewLoading:true,error:undefined});if(!owns())return;
    try{const preview=await previewLibraryChannel(this.transport(owns),this.serverId,config,read.signal);if(owns())this.publish({preview:frozen(preview)});}
    catch(error){if(owns())this.failure(error,'preview');}
    finally{if(this.previewRead===read){this.previewRead=undefined;this.publish({previewLoading:false});}}
  };
  /** The mounted composition invokes this synchronously on reader publication,
   * before its UI subscribers can observe a replaced row. An immutable pending
   * intent is never rebased or lent the directory's newer revision. */
  reconcileQuietly=():QuietRefreshOutcome=>{
    if(!this.current()||this.quietBlocked())return {kind:'deferred'};
    const original=this.original,config=this.state.config;
    if(!original||!config||this.state.phase!=='editing')return {kind:'adopted'};
    const fresh=this.reader.getSnapshot().channels?.find(row=>row.config.id===original.config.id);
    // This is a tolerant list: absence alone cannot retire this exact opening.
    if(!fresh)return {kind:'deferred'};
    if(fresh.revision<this.revision)return {kind:'deferred'};
    if(fresh===original)return {kind:'adopted'};
    const rebased=rebaseDraft(original.config,config,fresh.config);
    if(rebased.conflict){this.quietConflict=true;return {kind:'conflict'};}
    this.original=fresh;this.revision=fresh.revision;this.quietConflict=false;
    if(JSON.stringify(rebased.merged)!==JSON.stringify(config))this.publish({config:frozen(clonePortableDto(rebased.merged))});
    return {kind:'adopted'};
  };
  /** A tolerant list omission alone cannot close a dirty exact-target opening. */
  refreshQuietly=async():Promise<QuietRefreshOutcome>=>{
    const result=this.reconcileQuietly(),original=this.original;
    if(result.kind!=='deferred'||!this.current()||this.quietBlocked()||!original||this.reader.getSnapshot().channels?.some(row=>row.config.id===original.config.id))return result;
    this.invalidateQuiet();const read=this.quietRead=new AbortController(),rows=this.reader.getSnapshot().channels;
    const owns=()=>this.current()&&!read.signal.aborted&&this.quietRead===read&&this.original===original&&this.reader.getSnapshot().channels===rows&&!this.quietBlocked()&&this.state.phase==='editing';
    try{
      const raw=await this.transport(owns).request(encodedRoutePath('get_admin_library_channels_id_health',{id:encodeURIComponent(original.config.id)}),'GET',undefined,read.signal);
      if(!owns())return {kind:'deferred'};
      const response=decodeGetAdminLibraryChannelsIdHealthResponse(raw);
      if(response.serverId!==this.serverId||response.result.channelId!==original.config.id)throw new Error('invalid_channel_health_receipt');
      return {kind:'deferred'};
    }catch(error){
      if(!owns())return {kind:'deferred'};
      if((error as {status?:number}|null)?.status===404){this.close();this.original=undefined;this.publish({error:'changed'});return {kind:'adopted'};}
      if(terminal(error)){this.retire();this.reportError(error);}
      return {kind:'unreadable',error};
    }finally{if(this.quietRead===read)this.quietRead=undefined;}
  };
  save=async():Promise<LibraryChannel|undefined>=>{
    const config=this.state.config;if(!this.current()||this.flight||this.intent||!config||this.state.phase!=='editing')return;
    if(this.original&&!this.quietConflict&&!this.reader.getSnapshot().channels?.includes(this.original)){this.publish({error:'changed'});return;}
    let intent:Intent;try{intent=Object.freeze({config:frozen(clonePortableDto(config)),revision:this.revision,requestId:this.operationId()});}catch(error){this.failure(error,'save');return;}
    if(!this.current()||this.state.config!==config||this.flight||this.intent)return;this.intent=intent;return this.performSave(intent);
  };
  retry=async():Promise<LibraryChannel|undefined>=>{const intent=this.intent;return intent?this.performSave(intent):undefined;};
  private async performSave(intent:Intent):Promise<LibraryChannel|undefined>{
    if(!this.current()||this.flight||this.intent!==intent)return;
    this.invalidateQuiet();this.reader.invalidateQuiet();this.previewRead?.abort();const flight=new AbortController();this.flight=flight;const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    this.publish({busy:true,pending:true,error:undefined,previewLoading:false});let saved:LibraryChannel|undefined;
    try{if(!owns())return;const response=await saveLibraryChannel(this.transport(owns),this.serverId,intent.config,intent.revision,intent.requestId,flight.signal);if(!owns())return;
      if(response.config.id!==intent.config.id||response.revision<=intent.revision)throw new Error('invalid_channel_save_receipt');
      saved=response;this.intent=undefined;this.original=undefined;this.revision=response.revision;this.opening?.abort();this.opening=undefined;this.publish({phase:'closed',pending:false,config:frozen(clonePortableDto(response.config))});
    }catch(error){if(owns()){const status=(error&&typeof error==='object'?error:{}) as {status?:number};if([400,412,422].includes(status.status??0)){this.intent=undefined;this.publish({pending:false});}this.failure(error,status.status===412?'changed':'save');}}
    finally{if(this.flight===flight){this.flight=undefined;this.publish({busy:false});}}
    if(saved&&this.current())await this.reader.load();return this.current()?saved:undefined;
  }
  resume=()=>{if(this.current()&&!this.flight&&this.intent)this.publish({phase:'editing'});};
  close=()=>{if(this.flight)return;this.invalidateQuiet();this.inputActive=false;this.incomplete.clear();this.opening?.abort();this.opening=undefined;this.previewRead?.abort();this.previewRead=undefined;this.publish({phase:'closed',preview:undefined,previewLoading:false});};
  private retire(){this.invalidateQuiet();this.opening?.abort();this.previewRead?.abort();this.flight?.abort();this.flight=undefined;this.intent=undefined;this.lifetime.abort();this.state=empty;for(const listener of this.listeners)listener();}
  dispose=()=>{this.retire();this.listeners.clear();};
}
