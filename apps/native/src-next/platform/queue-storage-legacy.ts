import {NativeModules} from 'react-native';
/** The app's small durable records (servers, preferences, the v1 queue id…) in the native
 * Keychain-backed store: one atomic read-modify-write per key. */
// All hosts in this JS runtime serialize access to each native Keychain record.
// A native upsert completes durably before the next transaction may read it.
const tails=new Map<string,Promise<unknown>>();
/** The native store's answer for a record that is there but is not one it could have written
 * (both platforms). Any other rejection means it could not be read right now. */
const NATIVE_RECORD_DAMAGED='secure_record_damaged';
/** For a caller that must tell a record that can never be read (the native store says so, or its
 * text is not JSON) from a store that cannot be read right now. `error` is what a read reports.
 * With `replace`, a change sees the damaged record as absent: a record it returns is saved over
 * it, and returning none leaves it exactly as it is. */
export type DamagedRecord=Readonly<{error:()=>Error;replace?:boolean}>;
export class NativeQueueStore {
 change<T>(key:string,update:(raw:unknown)=>{record:unknown;value:T},damage?:DamagedRecord):Promise<T>{
  const run=(tails.get(key)??Promise.resolve()).catch(()=>{}).then(async()=>{
   // Without `damage` every failure is thrown as it is, as it always was.
   let raw:string|null|undefined,current:unknown=null,damaged=false;
   try{raw=await NativeModules.PorticoRuntime.loadQueueRecord(key);}
   catch(e){if(!damage||(e as {code?:unknown}|null)?.code!==NATIVE_RECORD_DAMAGED)throw e;damaged=true;}
   if(raw!=null){try{current=JSON.parse(raw);}catch(e){if(!damage)throw e;damaged=true;}}
   if(damaged&&!damage?.replace)throw damage!.error();
   const out=update(damaged?null:current);const json=JSON.stringify(out.record??null);
   if(json.length>262144)throw new Error('This could not be saved on this device.');
   if(damaged){if(out.record==null)return out.value;}
   // A read (or any change that leaves the record as it was) writes nothing: reading a saved
   // credential must never be able to damage it.
   else if(json===(raw??'null')||(raw!=null&&json===JSON.stringify(JSON.parse(raw))))return out.value;
   await NativeModules.PorticoRuntime.saveQueueRecord(key,json);return out.value;
  });
  tails.set(key,run);void run.finally(()=>{if(tails.get(key)===run)tails.delete(key);}).catch(()=>{});return run;
 }
}
