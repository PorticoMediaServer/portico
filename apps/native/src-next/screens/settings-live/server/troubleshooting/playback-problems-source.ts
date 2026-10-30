import {clearPlaybackFailureMemory,groupPlaybackFailures,readPlaybackFailureMemory,type PlaybackFailureGroup} from '../../../../../../../packages/client-core/src/playback-failure-memory';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import {NativeScreenLifetime,screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
type Snapshot=Readonly<{phase:'idle'|'loading'|'ready'|'error';groups:readonly PlaybackFailureGroup[];more:boolean;busy?:PlaybackFailureGroup}>;
const empty:Snapshot=Object.freeze({phase:'idle',groups:[],more:false});
function terminal(error:unknown){if(!error||typeof error!=='object')return false;return 'status' in error&&(error.status===401||error.status===403)||'code' in error&&(error.code==='unauthorized'||error.code==='forbidden'||error.code==='not_permitted');}
/** Only the existing remembered-method read/forget API; no playback policy or engine changes. */
export class NativePlaybackProblemsSource {
  private state=empty;
  private lifetime:NativeScreenLifetime;
  private listeners=new Set<()=>void>();
  private flight?:AbortController;
  private disposed=false;
  constructor(private context:NativeShellBootstrapContext,private reportError:(error:unknown)=>void){this.lifetime=new NativeScreenLifetime(context);}
  current=()=>!this.disposed&&this.lifetime.current()&&nativeStorageOwner(this.context);
  getSnapshot=()=>this.current()?this.state:empty;
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private publish(state:Snapshot){if(!this.current())return;this.state=Object.freeze(state);for(const listener of this.listeners)listener();}
  private api(flight:AbortController){return {request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>this.current()&&this.flight===flight&&!flight.signal.aborted&&!caller?.aborted?this.lifetime.request(signal=>this.context.api.request<T>(path,method,body,signal),flight.signal):Promise.reject(screenScopeEnded())};}
  private failed(error:unknown,owns:()=>boolean){if(!owns())return;if(terminal(error)){this.dispose();this.reportError(error);}else this.publish({phase:'error',groups:[],more:false});}
  load=async()=>{
    if(!this.current()||this.flight)return;const flight=new AbortController();this.flight=flight;
    const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    this.publish({phase:'loading',groups:[],more:false});
    try{if(!owns())return;const result=await readPlaybackFailureMemory(this.api(flight),flight.signal);if(owns())this.publish({phase:'ready',groups:Object.freeze(groupPlaybackFailures(result.items)),more:result.more});}
    catch(error){this.failed(error,owns);}finally{if(this.flight===flight)this.flight=undefined;}
  };
  forget=async(group:PlaybackFailureGroup)=>{
    if(!this.current()||this.flight||this.state.phase!=='ready'||!this.state.groups.includes(group))return;
    const flight=new AbortController();this.flight=flight;
    const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    this.publish({...this.state,busy:group});
    try{
      if(!owns()||!this.state.groups.includes(group))return;
      await clearPlaybackFailureMemory(this.api(flight),group.sourceId,flight.signal);
      if(!owns())return;
      // Once submitted, this list is unadmitted. Refresh only; never replay a DELETE.
      this.publish({phase:'loading',groups:[],more:false});if(!owns())return;
      const result=await readPlaybackFailureMemory(this.api(flight),flight.signal);
      if(owns())this.publish({phase:'ready',groups:Object.freeze(groupPlaybackFailures(result.items)),more:result.more});
    }catch(error){this.failed(error,owns);}finally{if(this.flight===flight)this.flight=undefined;}
  };
  dispose=()=>{this.disposed=true;this.flight?.abort();this.flight=undefined;this.lifetime.dispose();this.state=empty;this.listeners.clear();};
}
