import {sameUserMutationScope,type UserMutationEntry,type UserMutationScope} from '../../../../packages/client-core/src/user-mutations';

type Coordinator={snapshot():readonly UserMutationEntry[];subscribe(listener:()=>void):()=>void;reconcile():Promise<void>};
/** Explicit native Stop consumer. The native bridge proves the original renderer
 * has retired and persists that fact; core settles shared waiters before release. */
export class NativeStopResultConsumer {
 private alive=true;private readonly pending=new Map<string,Promise<void>>();private readonly off:()=>void;
 constructor(private readonly options:{coordinator:Coordinator;scope:()=>UserMutationScope|undefined;consumeLocal:(key:string)=>Promise<boolean>;consumeResult:(key:string)=>Promise<boolean>;report:(error:unknown)=>void}){
  this.off=options.coordinator.subscribe(()=>this.refresh());this.refresh();
 }
 refresh():void{
  if(!this.alive)return;
  const scope=this.options.scope();if(!scope)return;
  for(const entry of this.options.coordinator.snapshot()){
   if(entry.operation!=='stop_playback_session'||entry.state!=='confirmed'||!entry.receipt||!entry.nativeStopOwner||!sameUserMutationScope(scope,entry.scope)||this.pending.has(entry.key))continue;
   const task=Promise.resolve().then(async()=>{
    if(!this.alive||!await this.options.consumeLocal(entry.key)||!this.alive)return;
    await this.options.coordinator.reconcile();
    const current=this.options.scope(),saved=this.options.coordinator.snapshot().find(value=>value.key===entry.key);
    if(this.alive&&current&&saved?.state==='confirmed'&&saved.nativeStopConsumed===true&&sameUserMutationScope(current,saved.scope))await this.options.consumeResult(entry.key);
   }).catch(error=>this.options.report(error)).finally(()=>this.pending.delete(entry.key));
   this.pending.set(entry.key,task);
  }
 }
 async settled():Promise<void>{await Promise.all(this.pending.values());}
 dispose():void{this.alive=false;this.off();}
}
