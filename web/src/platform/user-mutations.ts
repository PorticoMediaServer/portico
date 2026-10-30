import {emptyUserMutationState,validateUserMutationState,type DurableUserMutationStore,type UserMutationState,UserMutationError} from '../../../packages/client-core/src/user-mutations.ts';

/** Nonsecret HTTP-LAN custody. Compare and replacement share ONE IndexedDB
 * transaction, including across tabs. Neither Web Locks nor subtle is used. */
export class IndexedDBUserMutationStore implements DurableUserMutationStore {
 private database:Promise<IDBDatabase>|undefined;
 constructor(private readonly factory:IDBFactory=globalThis.indexedDB,private readonly name='portico.user-mutations.v1'){}
 private open():Promise<IDBDatabase>{
  if(!this.factory)return Promise.reject(new UserMutationError('mutation_store_unavailable'));
  if(this.database)return this.database;
  this.database=new Promise((resolve,reject)=>{
   let request:IDBOpenDBRequest;try{request=this.factory.open(this.name,1);}catch{reject(new UserMutationError('mutation_store_unavailable'));return;}let refused=false;
   request.onupgradeneeded=()=>request.result.createObjectStore('state');
   request.onerror=()=>reject(new UserMutationError('mutation_store_unavailable'));
   request.onblocked=()=>{refused=true;reject(new UserMutationError('mutation_store_unavailable'));};
   request.onsuccess=()=>{const db=request.result;if(refused){db.close();return;}db.onversionchange=()=>{db.close();this.database=undefined;};resolve(db);};
  });
  void this.database.catch(()=>{this.database=undefined;});return this.database;
 }
 async read():Promise<UserMutationState>{
  const db=await this.open();return new Promise((resolve,reject)=>{
   let result:UserMutationState;let failure:unknown;
   let tx:IDBTransaction;try{tx=db.transaction('state','readonly');}catch{reject(new UserMutationError('mutation_store_unavailable'));return;}
   const request=tx.objectStore('state').get('current');
   request.onsuccess=()=>{try{result=request.result===undefined?emptyUserMutationState():validateUserMutationState(request.result);}catch(error){failure=error;tx.abort();}};
   tx.oncomplete=()=>resolve(result);tx.onabort=()=>reject(failure??new UserMutationError('mutation_store_unavailable'));tx.onerror=()=>{};
  });
 }
 async compareAndSwap(expectedRevision:number,next:UserMutationState):Promise<boolean>{
  // Capture synchronously: a caller cannot alter the replacement while open waits.
  next=validateUserMutationState(JSON.parse(JSON.stringify(next)));if(next.revision!==expectedRevision+1)throw new UserMutationError('mutation_store_damaged');
  const db=await this.open();return new Promise((resolve,reject)=>{
   let changed=false,failure:unknown;let tx:IDBTransaction;
   try{tx=db.transaction('state','readwrite',{durability:'strict'});if(tx.durability!=='strict'){tx.abort();reject(new UserMutationError('mutation_store_unavailable'));return;}}
   catch{reject(new UserMutationError('mutation_store_unavailable'));return;}
   const store=tx.objectStore('state'),request=store.get('current');
   request.onsuccess=()=>{try{const current=request.result===undefined?emptyUserMutationState():validateUserMutationState(request.result);if(current.revision!==expectedRevision)return;store.put(next,'current');changed=true;}catch(error){failure=error;tx.abort();}};
   tx.oncomplete=()=>resolve(changed);tx.onabort=()=>reject(failure??new UserMutationError('mutation_store_unavailable'));tx.onerror=()=>{};
  });
 }
 async close():Promise<void>{const database=this.database;this.database=undefined;if(database)(await database).close();}
}
