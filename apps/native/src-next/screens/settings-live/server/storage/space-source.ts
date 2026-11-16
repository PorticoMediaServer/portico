import {readServerStorage, storageUsageLines, type StorageUsageLine} from '../../../../../../../packages/client-core/src/server-admin/server-storage';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
type DataUsage = Readonly<{stateDirectory:string;onNetwork:boolean;folders:readonly Readonly<{name:string;bytes:number}>[];lines:readonly StorageUsageLine[]}>;
type Snapshot = Readonly<{phase:'idle'|'loading'|'ready'|'error';usage?:DataUsage}>;
const empty:Snapshot=Object.freeze({phase:'idle'});
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
    this.publish({phase:'loading'});
    try {
      if(!owns())return;
      // The data folder's disk and what uses it (rebuilt GET /v1/admin/storage).
      const storage=await this.lifetime.request(signal=>readServerStorage(this.context.api,signal),flight.signal);
      if(!owns())return;
      this.publish({phase:'ready',usage:Object.freeze({stateDirectory:storage.dataFolder,onNetwork:storage.sharedDrive,folders:Object.freeze((storage.folders??[]).map(folder=>Object.freeze({name:folder.name,bytes:folder.bytes}))),lines:storageUsageLines(storage)})});
    }catch(error){if(owns()){if(terminal(error)){this.dispose();this.reportError(error);}else this.publish({phase:'error'});}}
    finally{if(this.flight===flight)this.flight=undefined;}
  };
  dispose=()=>{this.flight?.abort();this.flight=undefined;this.lifetime.dispose();this.state=empty;this.listeners.clear();};
}
