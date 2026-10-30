import {sameUserMutationScope,UserMutationError,type UserMutationScope} from '../../../../packages/client-core/src/user-mutations';

type Store={activate(scope:UserMutationScope):Promise<void>;retire(scope:UserMutationScope):Promise<boolean>};
/** The durable selected identity owns custody. Component replacement, refreshed
 * tokens and authorization horizons do not end that identity's pending work. */
export class NativeUserMutationSelectionOwner {
 private binding?:{store:Store;scope:UserMutationScope;report:(error:unknown)=>void};
 constructor(private readonly withSelection:<T>(work:(scope:UserMutationScope|undefined)=>Promise<T>)=>Promise<T>,watch:(changed:()=>void)=>void){
  watch(()=>{void this.check().catch(error=>this.binding?.report(error));});
 }
 async activate(store:Store,scope:UserMutationScope,report:(error:unknown)=>void):Promise<void>{
  await this.withSelection(async selected=>{
   if(!selected||!sameUserMutationScope(selected,scope))throw new UserMutationError('mutation_scope_changed');
   await store.activate(scope);this.binding={store,scope:Object.freeze({...scope}),report};
  });
 }
 private async check():Promise<void>{
  await this.withSelection(async selected=>{
   const binding=this.binding;if(!binding||selected&&sameUserMutationScope(binding.scope,selected))return;
   // Native compares activeScope while holding its aggregate transaction. A late
   // old selection observer cannot retire a newer mounted family's custody.
   await binding.store.retire(binding.scope);
   if(this.binding===binding)this.binding=undefined;
  });
 }
}
