import {clonePortableDto} from '../clone-portable-dto.ts';
import {linkAbortSignals, throwIfAborted} from '../abort-lifetime.ts';
import type {ChannelApi} from '../channel-guide.ts';
import {browseCapabilitiesPath, parseBrowseCapabilities, validateBrowseQuery, type BrowseNode} from '../browse.ts';
import {channelConditionFields} from '../server-admin/channel-conditions.ts';
import {parseLibraryChannelConfig, type LibraryChannelBlock, type LibraryChannelConfig} from '../library-channels.ts';
import {channelRequestID} from '../live-source-draft.ts';
import {decodeGetAdminLibraryChannelsBlockPresetsResponse, decodePostAdminLibraryChannelsBlockPresetsResponse, decodePutAdminLibraryChannelsBlockPresetsIdResponse, decodeDeleteAdminLibraryChannelsBlockPresetsIdResponse} from '../../../contracts/src/decoders.ts';
import {encodedRoutePath, routePath} from '../../../contracts/src/route-paths.ts';
import type {GetAdminLibraryChannelsBlockPresetsResponse} from '../../../contracts/src/types.ts';
export type BlockPreset = GetAdminLibraryChannelsBlockPresetsResponse['result']['items'][number];
export type BlockPresetBody = BlockPreset['body'];
type Intent = Readonly<{id?:string; body:Readonly<{expectedRevision:number;operationId:string;name:string;body:BlockPresetBody}>}>;
type Snapshot = Readonly<{items:readonly BlockPreset[];cursor?:string;loaded:boolean;applicationOrders?:readonly string[];busy:boolean;pending?:Intent;reconcile:boolean;error?:boolean}>;
const empty:Snapshot=Object.freeze({items:Object.freeze([]),loaded:false,busy:false,reconcile:false});
const terminal=(error:unknown)=>{const e=(error&&typeof error==='object'?error:{}) as {status?:number;code?:string};return e.status===401||e.status===403||['unauthorized','forbidden','not_permitted'].includes(e.code??'');};
function freeze<T>(value:T):T {if(value&&typeof value==='object'){for(const child of Object.values(value))freeze(child);Object.freeze(value);}return value;}
const safe=(value:string,max:number)=>value.length<=max&&!/[\x00-\x1f\x7f]/.test(value);
const days=['monday','tuesday','wednesday','thursday','friday','saturday','sunday'];
const orders=['shuffle','sequential','recent','least-played'];
/** Presets are independent stored definitions. Channel application copies a
 * definition; this source never mutates an existing channel implicitly. */
export class BlockPresetsSource {
  private state=empty;
  private readonly lifetime=new AbortController();
  private readonly listeners=new Set<()=>void>();
  private flight?:AbortController;
  private intent?:Intent;
  constructor(private readonly api:ChannelApi,private readonly serverId:string,private readonly isCurrent:()=>boolean,private readonly reportError:(error:unknown)=>void,private readonly operationId:()=>string=channelRequestID){}
  private async request<T = unknown>(signals: readonly (AbortSignal | undefined)[], path: string, method?: string, body?: unknown): Promise<T> {
    const lifetime = linkAbortSignals(signals);
    try {throwIfAborted(lifetime.signal); return await this.api.request<T>(path, method, body, lifetime.signal);}
    finally {lifetime.dispose();}
  }
  private current(){return !this.lifetime.signal.aborted&&this.isCurrent();}
  getSnapshot=()=>this.current()?this.state:empty;
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private publish(patch:Partial<Snapshot>){if(this.current()){this.state=Object.freeze({...this.state,...patch});for(const listener of this.listeners)listener();}}
  private checked<T extends {serverId:string;protocolVersion:string;result:unknown}>(value:T){if(value.serverId!==this.serverId||value.protocolVersion!=='1.0')throw new Error('invalid_block_preset_scope');return value.result;}
  private failure(error:unknown){if(terminal(error)){this.dispose();this.reportError(error);}else this.publish({error:true});}
  load=async(more=false):Promise<void>=>{
    if(!this.current()||this.flight||this.intent||more&&!this.state.cursor)return;
    const before=this.state.items,cursor=more?this.state.cursor:undefined,flight=new AbortController();this.flight=flight;
    const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    this.publish({busy:true,error:undefined});
    try {
      if(!owns())return;
      const params=new URLSearchParams({limit:'100'});if(cursor)params.set('cursor',cursor);
      const response=decodeGetAdminLibraryChannelsBlockPresetsResponse(await this.request([flight.signal,this.lifetime.signal], routePath('get_admin_library_channels_block_presets')+'?'+params,'GET',undefined));
      if(!owns())return;
      const page=this.checked(response) as typeof response.result;
      if(page.items.length>100||page.nextCursor.length>2048||cursor&&page.nextCursor===cursor)throw new Error('invalid_block_preset_page');
      const applicationOrders=page.enumerations.applicationOrder;
      if(applicationOrders!==undefined&&(applicationOrders.length>4||new Set(applicationOrders).size!==applicationOrders.length||applicationOrders.some(order=>!orders.includes(order))))throw new Error('invalid_block_preset_application_orders');
      const items=more?[...before,...page.items]:page.items;
      if(items.length>2000||new Set(items.map(item=>item.id)).size!==items.length||items.some(item=>!this.validRecord(item)))throw new Error('invalid_block_preset_page');
      this.publish({items:freeze(clonePortableDto(items)),cursor:page.nextCursor||undefined,loaded:true,applicationOrders:applicationOrders===undefined?undefined:Object.freeze([...applicationOrders]),reconcile:false});
    }catch(error){if(owns())this.failure(error);}finally{if(this.flight===flight){this.flight=undefined;this.publish({busy:false});}}
  };
  private validBody(body:BlockPresetBody){return safe(body.libraryId,128)&&safe(body.description,400)&&orders.includes(body.order)&&Number.isInteger(body.durationMinutes)&&body.durationMinutes>=5&&body.durationMinutes<=1440&&Number.isInteger(body.startMinute)&&body.startMinute>=-1&&body.startMinute<=1439&&body.days.length<=7&&new Set(body.days).size===body.days.length&&body.days.every(day=>days.includes(day))&&body.criteria.length<=16&&body.criteria.every(row=>!!row.field&&safe(row.field,64)&&['includes','excludes'].includes(row.operator)&&row.values.length>0&&row.values.length<=200&&row.values.every(value=>!!value&&safe(value,200)));}
  private sameBody(a:BlockPresetBody,b:BlockPresetBody){return a.libraryId===b.libraryId&&a.description===b.description&&a.order===b.order&&a.durationMinutes===b.durationMinutes&&a.startMinute===b.startMinute&&JSON.stringify(a.days)===JSON.stringify(b.days)&&a.criteria.length===b.criteria.length&&a.criteria.every((row,index)=>{const other=b.criteria[index]!;return row.field===other.field&&row.operator===other.operator&&JSON.stringify(row.values)===JSON.stringify(other.values);});}
  private validRecord(row:BlockPreset){return /^[A-Za-z0-9_-]{1,128}$/.test(row.id)&&!!row.name&&safe(row.name,120)&&Number.isSafeInteger(row.revision)&&row.revision>0&&this.validBody(row.body);}
  save=async(original:BlockPreset|undefined,name:string,body:BlockPresetBody):Promise<boolean>=>{
    if(!this.current()||this.flight||this.intent||this.state.reconcile||!this.state.loaded||original&&!this.state.items.includes(original)||!name.trim()||!safe(name,120)||!this.validBody(body))return false;
    const list=this.state.items;let intent:Intent;
    try{intent=freeze({id:original?.id,body:{expectedRevision:original?.revision??0,operationId:this.operationId(),name,body:clonePortableDto(body)}});}catch{this.publish({error:true});return false;}
    if(!this.current()||this.flight||this.intent||this.state.items!==list||original&&!list.includes(original))return false;
    this.intent=intent;return this.run(intent);
  };
  retry=()=>this.intent?this.run(this.intent):Promise.resolve(false);
  private async run(intent:Intent):Promise<boolean>{
    if(!this.current()||this.flight||this.intent!==intent)return false;
    const flight=new AbortController();this.flight=flight;const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted&&this.intent===intent;
    this.publish({busy:true,pending:intent,error:undefined});let completed=false;
    try {
      if(!owns())return false;
      const raw=await this.request([flight.signal,this.lifetime.signal], intent.id?encodedRoutePath('put_admin_library_channels_block_presets_id',{id:encodeURIComponent(intent.id)}):routePath('post_admin_library_channels_block_presets'),intent.id?'PUT':'POST',intent.body);
      if(!owns())return false;
      const response=intent.id?decodePutAdminLibraryChannelsBlockPresetsIdResponse(raw):decodePostAdminLibraryChannelsBlockPresetsResponse(raw),row=this.checked(response) as BlockPreset;
      const expected={...intent.body.body,days:[...intent.body.body.days].sort()};
      if(!this.validRecord(row)||intent.id&&row.id!==intent.id||row.revision!==intent.body.expectedRevision+1||row.name!==intent.body.name||!this.sameBody(row.body,expected))throw new Error('invalid_block_preset_receipt');
      this.intent=undefined;completed=true;this.publish({pending:undefined,loaded:false});
    }catch(error){if(owns()){const status=(error as {status?:number}|null)?.status;if(status===400||status===409||status===422){this.intent=undefined;this.publish({pending:undefined});}this.failure(error);}}
    finally{if(this.flight===flight){this.flight=undefined;this.publish({busy:false});}}
    if(completed&&this.current())await this.load();return completed;
  }
  remove=async(record:BlockPreset):Promise<boolean>=>{
    if(!this.current()||this.flight||this.intent||this.state.reconcile||!this.state.loaded||!this.state.items.includes(record))return false;
    let operationId:string;try{operationId=this.operationId();}catch{this.publish({error:true});return false;}
    const list=this.state.items;
    if(!this.current()||this.flight||this.intent||this.state.items!==list||!list.includes(record))return false;
    const flight=new AbortController();this.flight=flight;const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    this.publish({busy:true,error:undefined});let completed=false;
    try {
      if(!owns())return false;
      const query=new URLSearchParams({expectedRevision:String(record.revision),operationId});
      const response=decodeDeleteAdminLibraryChannelsBlockPresetsIdResponse(await this.request([flight.signal,this.lifetime.signal], encodedRoutePath('delete_admin_library_channels_block_presets_id',{id:encodeURIComponent(record.id)})+'?'+query,'DELETE',undefined));
      if(!owns())return false;
      const result=this.checked(response) as typeof response.result;
      if(!result.deleted||result.id!==record.id)throw new Error('invalid_block_preset_delete');completed=true;this.publish({loaded:false});
    }catch(error){if(owns()){this.publish({reconcile:true});this.failure(error);}}
    finally{if(this.flight===flight){this.flight=undefined;this.publish({busy:false});}}
    if(completed&&this.current())await this.load();return completed;
  };
  /** Read-only application: copy the checked definition into the caller's
   * unsaved channel. Later preset writes cannot change this copied rule. */
  apply=async(record:BlockPreset,config:LibraryChannelConfig,block:LibraryChannelBlock,caller:AbortSignal,callerCurrent:()=>boolean):Promise<LibraryChannelConfig|undefined>=>{
    if(!this.current()||this.flight||this.intent||!this.state.loaded||this.state.reconcile||caller.aborted||!callerCurrent()||!this.state.items.includes(record)||record.body.order==='least-played'&&!this.state.applicationOrders?.includes('least-played')||!record.body.libraryId||config.rules.length>=16)return;
    let ruleId:string,definition:BlockPresetBody,baseline:LibraryChannelConfig;
    try{ruleId='rule-'+this.operationId().slice(0,24);definition=freeze(clonePortableDto(record.body));baseline=clonePortableDto(config);}catch{return;}
    const list=this.state.items;
    if(!this.current()||this.flight||this.intent||caller.aborted||!callerCurrent()||this.state.items!==list||!list.includes(record))return;
    const flight=new AbortController();this.flight=flight;
    const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted&&!caller.aborted&&callerCurrent()&&this.state.items===list&&list.includes(record);
    this.publish({busy:true,error:undefined});
    try{
      if(!owns())return;
      const raw=await this.request([flight.signal,this.lifetime.signal,caller], browseCapabilitiesPath(definition.libraryId, ''),'GET',undefined);
      if(!owns())return;
      const capabilities=parseBrowseCapabilities(raw);
      if(capabilities.library.id!==definition.libraryId||!['movie','tv','anime'].includes(capabilities.library.kind))throw new Error('unsupported_block_preset_library');
      const fields=channelConditionFields(capabilities.fields),clauses:BrowseNode[]=[];
      for(const clause of definition.criteria){
        const field=fields.find(field=>field.id===clause.field);if(!field)throw new Error('unsupported_block_preset_field');
        const values=clause.values.map(value=>field.type==='number'?Number(value):field.type==='boolean'?value==='true'?true:value==='false'?false:value:value);
        const operator=field.operators.includes('contains-any')?'contains-any':field.operators.includes('in')?'in':field.operators.includes('equals')?'equals':undefined;
        if(!operator)throw new Error('unsupported_block_preset_operator');
        const node:BrowseNode=operator==='equals'&&values.length>1?{variant:'group' as const,any:values.map(value=>({variant:'predicate' as const,field:field.id,operator:'equals',value}))}:{variant:'predicate' as const,field:field.id,operator,value:operator==='equals'?values[0]!:values};
        validateBrowseQuery(capabilities,node);
        clauses.push(clause.operator==='excludes'?{variant:'group' as const,not:node}:node);
      }
      const filter:BrowseNode|undefined=clauses.length?{variant:'group' as const,all:clauses}:undefined;
      if(filter){validateBrowseQuery(capabilities,filter);const leaves=(node:BrowseNode):number=>'field'in node?1:'not'in node?leaves(node.not):('all'in node?node.all:node.any).reduce((sum,child)=>sum+leaves(child),0);if(leaves(filter)>capabilities.queryLimits.maximumClauses||new TextEncoder().encode(JSON.stringify(filter)).length>capabilities.queryLimits.maximumBytes)throw new Error('unsupported_block_preset_size');}
      const base=baseline.rules.find(rule=>rule.id===baseline.defaultRuleId);if(!base)throw new Error('invalid_block_preset_channel');
      const rule={...base,id:ruleId,name:record.name,mode:definition.order==='shuffle'?'shuffle-bag' as const:'sequential' as const,weights:[],query:{...base.query,libraryIds:[definition.libraryId],kinds:capabilities.library.kind==='movie'?['movie' as const]:['episode' as const],showIds:[],genres:[],yearFrom:0,yearThrough:0,recentDays:0,text:'',includeItemIds:[],excludeItemIds:[],filter,order:definition.order==='least-played'?'least-played' as const:definition.order==='recent'?'recent' as const:'title' as const,limit:0}};
      const result=parseLibraryChannelConfig({...baseline,rules:[...baseline.rules,rule],blocks:baseline.blocks.map(row=>row.id===block.id?{...row,ruleId}:row)});
      if(!owns())return;return freeze(result);
    }catch(error){if(owns())this.failure(error);return;}finally{if(this.flight===flight){this.flight=undefined;this.publish({busy:false});}}
  };
  dispose=()=>{this.flight?.abort();this.lifetime.abort();this.intent=undefined;this.state=empty;this.listeners.clear();};
}
