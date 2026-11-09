import {sameNativeStopScope,type NativeStopScope} from './user-mutations';

type Store={activate(scope:NativeStopScope):Promise<void>;retire(scope:NativeStopScope):Promise<boolean>};
/** The durable selected identity owns custody. Component replacement, refreshed
 * tokens and authorization horizons do not end that identity's pending work. */
export class NativeUserMutationSelectionOwner {
 private binding?:{store:Store;scope:NativeStopScope;report:(error:unknown)=>void};
 constructor(private readonly withSelection:<T>(work:(scope:NativeStopScope|undefined)=>Promise<T>)=>Promise<T>,watch:(changed:()=>void)=>void){
  watch(()=>{void this.check().catch(error=>this.binding?.report(error));});
 }
 async activate(store:Store,scope:NativeStopScope,report:(error:unknown)=>void):Promise<void>{
  await this.withSelection(async selected=>{
   if(!selected||!sameNativeStopScope(selected,scope))throw Object.assign(new Error('mutation_scope_changed'),{code:'mutation_scope_changed'});
   await store.activate(scope);this.binding={store,scope:Object.freeze({...scope}),report};
  });
 }
 private async check():Promise<void>{
  await this.withSelection(async selected=>{
   const binding=this.binding;if(!binding||selected&&sameNativeStopScope(binding.scope,selected))return;
   // Native compares activeScope while holding its aggregate transaction. A late
   // old selection observer cannot retire a newer mounted family's custody.
   await binding.store.retire(binding.scope);
   if(this.binding===binding)this.binding=undefined;
  });
 }
}
