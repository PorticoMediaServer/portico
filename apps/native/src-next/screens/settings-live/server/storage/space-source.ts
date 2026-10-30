import {decodeGetAdminStorageUsageResponse, decodeGetDataStorageUsageResponse} from '../../../../../../../packages/contracts/src/decoders';
import {routePath} from '../../../../../../../packages/contracts/src/routes';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';

type DataUsage = Readonly<{stateDirectory:string;onNetwork?:boolean;folders:readonly Readonly<{name:string;bytes:number}>[];transcodeDirectory:string;transcodeBytes:number;notices:readonly Readonly<{code:string;message:string}>[]}>;
type Snapshot = Readonly<{phase:'idle'|'loading'|'ready'|'error';usage?:DataUsage}>;
const empty:Snapshot=Object.freeze({phase:'idle'});
const text=(value:string,max:number)=>value.length<=max&&!/[\x00-\x1f\x7f]/.test(value);
const bytes=(value:number)=>Number.isSafeInteger(value)&&value>=0;
const terminal=(error:unknown)=>{const e=(error&&typeof error==='object'?error:{}) as {status?:number;code?:string};return e.status===401||e.status===403||['unauthorized','forbidden','not_permitted'].includes(e.code??'');};
export function nativeStorageOwner(context:NativeShellBootstrapContext):boolean {
  const authority=context.authority(),account=context.account;
  return context.current()&&account.serverId===authority.serverId&&account.account.id===authority.accountId&&account.account.role==='owner'&&!account.account.disabled&&account.capabilities.manageServer&&account.account.primaryProfileId===authority.profileId;
}
/** One committed phone viewport reads the real owner data-folder layout. A
 * maintenance category is never reinterpreted as a measured data folder. */
export class NativeStorageSpaceSource {
  private state=empty;
  private readonly lifetime:NativeScreenLifetime;
  private readonly listeners=new Set<()=>void>();
  private flight?:AbortController;
  constructor(private readonly context:NativeShellBootstrapContext,private readonly reportError:(error:unknown)=>void){this.lifetime=new NativeScreenLifetime(context);}
  private current(){return this.lifetime.current()&&nativeStorageOwner(this.context);}
  getSnapshot=()=>this.current()?this.state:empty;
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private publish(state:Snapshot){if(this.current()){this.state=Object.freeze(state);for(const listener of this.listeners)listener();}}
  load=async():Promise<void>=>{
    if(!this.current()||this.flight)return;
    const flight=new AbortController();this.flight=flight;
    const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    const read=(path:string)=>{if(!owns())return Promise.reject(screenScopeEnded());return this.lifetime.request(signal=>this.context.api.request<unknown>(path,'GET',undefined,signal),flight.signal);};
    this.publish({phase:'loading'});
    try {
      if(!owns())return;
      const [rawReport,rawUsage]=await Promise.all([read(routePath('get_admin_storage_usage')),read(routePath('get_data_storage_usage'))]);
      if(!owns())return;
      const report=decodeGetAdminStorageUsageResponse(rawReport),usage=decodeGetDataStorageUsageResponse(rawUsage);
      if(report.serverId!==this.lifetime.authority.serverId||report.protocolVersion!=='1.0'||!report.result.stateDirectory||usage.dataDirectory!==report.result.stateDirectory||!text(usage.dataDirectory,8192)||!text(usage.transcodeDirectory,8192)||!bytes(usage.transcodeBytes)||!usage.transcodeDirectory&&usage.transcodeBytes!==0||usage.folders.length>64||usage.notices.length>32||new Set(usage.folders.map(folder=>folder.name)).size!==usage.folders.length||usage.folders.some(folder=>!folder.name||!text(folder.name,120)||!bytes(folder.bytes))||usage.notices.some(notice=>!text(notice.code,120)||!text(notice.message,2048)))throw new Error('invalid_storage_layout');
      const total=usage.folders.reduce((sum,folder)=>sum+folder.bytes,usage.transcodeBytes);if(!bytes(total))throw new Error('invalid_storage_layout');
      this.publish({phase:'ready',usage:Object.freeze({stateDirectory:usage.dataDirectory,...(report.result.stateDirectoryOnNetwork===undefined?{}:{onNetwork:report.result.stateDirectoryOnNetwork}),folders:Object.freeze(usage.folders.map(folder=>Object.freeze({...folder}))),transcodeDirectory:usage.transcodeDirectory,transcodeBytes:usage.transcodeBytes,notices:Object.freeze(usage.notices.map(notice=>Object.freeze({...notice})))})});
    }catch(error){if(owns()){if(terminal(error)){this.dispose();this.reportError(error);}else this.publish({phase:'error'});}}
    finally{if(this.flight===flight)this.flight=undefined;}
  };
  dispose=()=>{this.flight?.abort();this.flight=undefined;this.lifetime.dispose();this.state=empty;this.listeners.clear();};
}
